// Copyright © 2018 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package azure

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2018-02-01/resources"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2017-10-01/storage"
	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	pipelineAuth "github.com/banzaicloud/pipeline/auth"
	"github.com/banzaicloud/pipeline/internal/objectstore"
	"github.com/banzaicloud/pipeline/pkg/providers"
	pkgSecret "github.com/banzaicloud/pipeline/pkg/secret"
	"github.com/banzaicloud/pipeline/secret"
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Default storage account name when none is provided.
// This must between 3-23 letters and can only contain small letters and numbers.
const defaultStorageAccountName = "pipelinegenstorageacc"

var alfanumericRegexp = regexp.MustCompile(`[^a-zA-Z0-9]`)

type bucketNotFoundError struct{}

func (bucketNotFoundError) Error() string  { return "bucket not found" }
func (bucketNotFoundError) NotFound() bool { return true }

// ObjectStore stores all required parameters for container creation.
//
// Note: calling methods on this struct is not thread safe currently.
type ObjectStore struct {
	storageAccount string
	resourceGroup  string
	location       string
	secret         *secret.SecretItemResponse

	org *pipelineAuth.Organization

	db     *gorm.DB
	logger logrus.FieldLogger
	force  bool
}

// NewObjectStore returns a new object store instance.
func NewObjectStore(
	location string,
	resourceGroup string,
	storageAccount string,
	secret *secret.SecretItemResponse,
	org *pipelineAuth.Organization,
	db *gorm.DB,
	logger logrus.FieldLogger,
	force bool,
) *ObjectStore {
	return &ObjectStore{
		location:       location,
		resourceGroup:  resourceGroup,
		storageAccount: storageAccount,
		secret:         secret,
		db:             db,
		logger:         logger,
		org:            org,
		force:          force,
	}
}

// getResourceGroup returns the given resource group or generates one.
func (s *ObjectStore) getResourceGroup() string {
	resourceGroup := s.resourceGroup

	// generate a default resource group name if none given
	if resourceGroup == "" {
		resourceGroup = fmt.Sprintf("pipeline-auto-%s", s.location)
	}

	return resourceGroup
}

// getStorageAccount returns the given storage account or or falls back to a default one.
func (s *ObjectStore) getStorageAccount() string {
	storageAccount := s.storageAccount

	if storageAccount == "" {
		storageAccount = defaultStorageAccountName
	}

	return storageAccount
}

func (s *ObjectStore) getLogger(bucketName string) logrus.FieldLogger {
	return s.logger.WithFields(logrus.Fields{
		"organization":    s.org.ID,
		"bucket":          bucketName,
		"resource_group":  s.getResourceGroup(),
		"storage_account": s.getStorageAccount(),
	})
}

// CreateBucket creates an Azure Object Store Blob with the provided name
// within a generated/provided ResourceGroup and StorageAccount
func (s *ObjectStore) CreateBucket(bucketName string) error {
	resourceGroup := s.getResourceGroup()
	storageAccount := s.getStorageAccount()

	logger := s.getLogger(bucketName)

	bucket := &ObjectStoreBucketModel{}
	searchCriteria := s.searchCriteria(bucketName)

	dbr := s.db.Where(searchCriteria).Find(bucket)

	if dbr.Error != nil {
		if dbr.Error != gorm.ErrRecordNotFound {
			return errors.Wrap(dbr.Error, "error happened during getting bucket from DB: %s")
		}
	} else {
		return fmt.Errorf("bucket with name %s already exists", bucketName)
	}

	bucket.Name = bucketName
	bucket.ResourceGroup = resourceGroup
	bucket.StorageAccount = storageAccount
	bucket.Organization = *s.org
	bucket.SecretRef = s.secret.ID
	bucket.Status = providers.BucketCreating

	logger.Info("saving bucket in DB")

	err := s.db.Save(bucket).Error
	if err != nil {
		return errors.Wrap(err, "error happened during saving bucket in DB")
	}

	err = s.createResourceGroup(resourceGroup)
	if err != nil {
		return s.rollback(logger, "resource group creation failed", err, bucket)
	}

	// update here so the bucket list does not get inconsistent
	updateField := &ObjectStoreBucketModel{StorageAccount: s.storageAccount}
	err = s.db.Model(bucket).Update(updateField).Error
	if err != nil {
		return errors.Wrap(err, "error happened during updating storage account")
	}

	exists, err := s.checkStorageAccountExistence(resourceGroup, storageAccount)

	if err != nil {
		return s.rollback(logger, "error during creating the storage account:", err, bucket)
	}

	if !exists {
		err = s.createStorageAccount(resourceGroup, storageAccount)
		if err != nil {
			return s.rollback(logger, "storage account creation failed", err, bucket)
		}
	}

	key, err := GetStorageAccountKey(resourceGroup, storageAccount, s.secret, s.logger)
	if err != nil {
		return s.rollback(logger, "could not get storage account", err, bucket)
	}

	// update here so the bucket list does not get inconsistent
	updateField = &ObjectStoreBucketModel{Name: bucketName, Location: s.location}
	err = s.db.Model(bucket).Update(updateField).Error
	if err != nil {
		return s.rollback(logger, "error happened during updating bucket name", err, bucket)
	}

	p := azblob.NewPipeline(azblob.NewSharedKeyCredential(storageAccount, key), azblob.PipelineOptions{})
	URL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", storageAccount, bucketName))
	containerURL := azblob.NewContainerURL(*URL, p)

	_, err = containerURL.GetPropertiesAndMetadata(context.TODO(), azblob.LeaseAccessConditions{})
	if err != nil && err.(azblob.StorageError).ServiceCode() == azblob.ServiceCodeContainerNotFound {
		_, err = containerURL.Create(context.TODO(), azblob.Metadata{}, azblob.PublicAccessNone)
		if err != nil {
			return s.rollback(logger, "cannot access bucket", err, bucket)
		}
	}

	secretName, err := s.createUpdateStorageAccountSecret(key)
	if err != nil {
		return err
	}
	logger.Infof("storageAccount secret %v created/updated", secretName)
	bucket.Status = providers.BucketCreated

	err = s.db.Save(bucket).Error
	if err != nil {
		return s.rollback(logger, "could not save bucket", err, bucket)
	}

	return nil
}

func (s *ObjectStore) createUpdateStorageAccountSecret(accesskey string) (string, error) {
	storageAccount := s.getStorageAccount()

	storageAccountName := alfanumericRegexp.ReplaceAllString(storageAccount, "-")
	secretName := fmt.Sprintf("%v-key", storageAccountName)

	secretRequest := secret.CreateSecretRequest{
		Name: secretName,
		Type: "azureStorageAccount",
		Values: map[string]string{
			"storageAccount": storageAccount,
			"accessKey":      accesskey,
		},
		Tags: []string{
			fmt.Sprintf("azureStorageAccount:%v", storageAccount),
		},
	}
	if _, err := secret.Store.CreateOrUpdate(s.org.ID, &secretRequest); err != nil {
		return secretName, errors.Wrap(err, "failed to create/update secret: "+secretRequest.Name)
	}
	return secretName, nil
}

func (s *ObjectStore) rollback(logger logrus.FieldLogger, msg string, err error, bucket *ObjectStoreBucketModel) error {
	bucket.Status = providers.BucketCreateError
	bucket.StatusMsg = err.Error()
	e := s.db.Save(bucket).Error
	if e != nil {
		logger.Error(e.Error())
	}

	return errors.Wrapf(err, "%s", msg)
}

func (s *ObjectStore) deleteFailed(logger logrus.FieldLogger, msg string, err error, bucket *ObjectStoreBucketModel) error {
	bucket.Status = providers.BucketDeleteError
	bucket.StatusMsg = err.Error()
	e := s.db.Save(bucket).Error
	if e != nil {
		logger.Error(e.Error())
	}

	return errors.Wrapf(err, "%s", msg)
}

func (s *ObjectStore) createResourceGroup(resourceGroup string) error {
	logger := s.logger.WithField("resource_group", resourceGroup)
	gclient := resources.NewGroupsClient(s.secret.Values[pkgSecret.AzureSubscriptionId])

	logger.Info("creating resource group")

	authorizer, err := newAuthorizer(s.secret)
	if err != nil {
		return fmt.Errorf("authentication failed: %s", err.Error())
	}

	gclient.Authorizer = authorizer
	res, _ := gclient.Get(context.TODO(), resourceGroup)

	if res.StatusCode == http.StatusNotFound {
		result, err := gclient.CreateOrUpdate(
			context.TODO(),
			resourceGroup,
			resources.Group{Location: to.StringPtr(s.location)},
		)
		if err != nil {
			return err
		}

		logger.Info(result.Status)
	}

	logger.Info("resource group created")

	return nil
}

func (s *ObjectStore) checkStorageAccountExistence(resourceGroup string, storageAccount string) (bool, error) {
	storageAccountsClient, err := createStorageAccountClient(s.secret)
	if err != nil {
		return true, err
	}

	logger := s.logger.WithFields(logrus.Fields{
		"resource_group":  resourceGroup,
		"storage_account": storageAccount,
	})

	logger.Info("checking storage account availability")

	result, err := storageAccountsClient.CheckNameAvailability(
		context.TODO(),
		storage.AccountCheckNameAvailabilityParameters{
			Name: to.StringPtr(storageAccount),
			Type: to.StringPtr("Microsoft.Storage/storageAccounts"),
		},
	)
	if err != nil {
		return true, err
	}

	if *result.NameAvailable == false {
		if _, err = storageAccountsClient.GetProperties(context.TODO(), resourceGroup, storageAccount); err != nil {
			logger.Errorf("storage account name not available, %s", *result.Message)
			return true, fmt.Errorf(*result.Message)
		}
	}

	return false, nil
}

func (s *ObjectStore) createStorageAccount(resourceGroup string, storageAccount string) error {
	storageAccountsClient, err := createStorageAccountClient(s.secret)
	if err != nil {
		return err
	}

	logger := s.logger.WithFields(logrus.Fields{
		"resource_group":  resourceGroup,
		"storage_account": storageAccount,
	})

	logger.Info("creating storage account")

	future, err := storageAccountsClient.Create(
		context.TODO(),
		resourceGroup,
		storageAccount,
		storage.AccountCreateParameters{
			Sku: &storage.Sku{
				Name: storage.StandardLRS,
			},
			Kind:     storage.BlobStorage,
			Location: to.StringPtr(s.location),
			AccountPropertiesCreateParameters: &storage.AccountPropertiesCreateParameters{
				AccessTier: storage.Hot,
			},
		},
	)

	if err != nil {
		return fmt.Errorf("cannot create storage account: %v", err)
	}

	logger.Info("storage account creation request sent")
	if future.WaitForCompletion(context.TODO(), storageAccountsClient.Client) != nil {
		return err
	}

	logger.Info("storage account created")

	return nil
}

// DeleteBucket deletes the Azure storage container identified by the specified name
// under the current resource group, storage account provided the storage container is of 'managed' type.
func (s *ObjectStore) DeleteBucket(bucketName string) error {

	logger := s.getLogger(bucketName)

	bucket := &ObjectStoreBucketModel{}
	searchCriteria := s.searchCriteria(bucketName)

	logger.Infof("looking up the bucket %s", bucketName)

	if err := s.db.Where(searchCriteria).Find(bucket).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return bucketNotFoundError{}
		}
	}

	if err := s.deleteFromProvider(bucket); err != nil {
		if !s.force {
			// if delete is not forced return here
			return err
		}
	}

	db := s.db.Delete(bucket)
	if db.Error != nil {
		return s.deleteFailed(logger, db.Error.Error(), db.Error, bucket)
	}

	return nil

}

func (s *ObjectStore) deleteFromProvider(bucket *ObjectStoreBucketModel) error {
	logger := s.getLogger(bucket.Name)
	logger.Info("deleting bucket on provider")

	// todo the assumption here is, that a bucket in 'ERROR_CREATE' doesn't exist on the provider
	// todo however there might be -presumably rare cases- when a bucket in 'ERROR_DELETE' that has already been deleted on the provider
	if bucket.Status == providers.BucketCreateError {
		logger.Debug("bucket doesn't exist on provider")
		return nil
	}

	bucket.Status = providers.BucketDeleting
	db := s.db.Save(bucket)
	if db.Error != nil {
		return fmt.Errorf("could not update bucket: %s", bucket.Name)
	}

	key, err := GetStorageAccountKey(s.getResourceGroup(), s.getStorageAccount(), s.secret, s.logger)
	if err != nil {
		return s.deleteFailed(logger, "could not get account key", err, bucket)
	}

	p := azblob.NewPipeline(azblob.NewSharedKeyCredential(s.getStorageAccount(), key), azblob.PipelineOptions{})
	URL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", s.getStorageAccount(), bucket.Name))
	containerURL := azblob.NewContainerURL(*URL, p)

	if _, err = containerURL.Delete(context.TODO(), azblob.ContainerAccessConditions{}); err != nil {
		return s.deleteFailed(logger, "could not delete container", err, bucket)
	}

	return nil

}

// CheckBucket checks the status of the given Azure blob.
func (s *ObjectStore) CheckBucket(bucketName string) error {
	resourceGroup := s.getResourceGroup()
	storageAccount := s.getStorageAccount()

	logger := s.getLogger(bucketName)
	logger.Info("looking for bucket")

	_, err := s.checkStorageAccountExistence(resourceGroup, storageAccount)
	if err != nil {
		logger.Error(err.Error())
		return err
	}

	key, err := GetStorageAccountKey(resourceGroup, storageAccount, s.secret, s.logger)
	if err != nil {
		logger.Error(err.Error())

		return err
	}

	p := azblob.NewPipeline(azblob.NewSharedKeyCredential(s.storageAccount, key), azblob.PipelineOptions{})
	URL, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", s.storageAccount, bucketName))
	containerURL := azblob.NewContainerURL(*URL, p)

	_, err = containerURL.GetPropertiesAndMetadata(context.TODO(), azblob.LeaseAccessConditions{})
	if err != nil {
		return err
	}

	return nil
}

// ListBuckets returns a list of Azure storage containers buckets that can be accessed with the credentials
// referenced by the secret field. Azure storage containers buckets that were created by a user in the current
// org are marked as 'managed'.
func (s *ObjectStore) ListBuckets() ([]*objectstore.BucketInfo, error) {
	logger := s.logger.WithFields(logrus.Fields{
		"organization":    s.org.ID,
		"subscription_id": s.secret.GetValue(pkgSecret.AzureSubscriptionId),
	})

	logger.Info("getting all resource groups for subscription")

	resourceGroups, err := getAllResourceGroups(s.secret)
	if err != nil {
		return nil, fmt.Errorf("getting all resource groups failed: %s", err.Error())
	}

	var buckets []*objectstore.BucketInfo

	for _, rg := range resourceGroups {
		logger.WithField("resource_group", *(rg.Name)).Info("getting all storage accounts under resource group")

		storageAccounts, err := getAllStorageAccounts(s.secret, *rg.Name)
		if err != nil {
			return nil, fmt.Errorf("getting all storage accounts under resource group=%s failed: %s", *(rg.Name), err.Error())
		}

		// get all Blob containers under the storage account
		for i := 0; i < len(*storageAccounts); i++ {
			accountName := *(*storageAccounts)[i].Name

			logger.WithFields(logrus.Fields{
				"resource_group":  *(rg.Name),
				"storage_account": accountName,
			}).Info("getting all blob containers under storage account")

			accountKey, err := GetStorageAccountKey(*rg.Name, accountName, s.secret, s.logger)
			if err != nil {
				return nil, fmt.Errorf("getting all storage accounts under resource group=%s, storage account=%s failed: %s", *(rg.Name), accountName, err.Error())
			}

			blobContainers, err := getAllBlobContainers(accountName, accountKey)
			if err != nil {
				return nil, fmt.Errorf("getting all storage accounts under resource group=%s, storage account=%s failed: %s", *(rg.Name), accountName, err.Error())
			}

			for i := 0; i < len(blobContainers); i++ {
				blobContainer := blobContainers[i]

				bucketInfo := &objectstore.BucketInfo{
					Name:    blobContainer.Name,
					Managed: false,
					Azure: &objectstore.BlobStoragePropsForAzure{
						StorageAccount: accountName,
						ResourceGroup:  *rg.Name,
					},
				}

				buckets = append(buckets, bucketInfo)
			}

		}
	}

	var objectStores []ObjectStoreBucketModel

	err = s.db.Where(&ObjectStoreBucketModel{OrganizationID: s.org.ID}).Order("resource_group asc, storage_account asc, name asc").Find(&objectStores).Error
	if err != nil {
		return nil, fmt.Errorf("retrieving managed buckets failed: %s", err.Error())
	}

	for _, bucketInfo := range buckets {
		// managedAzureBlobStores must be sorted in order to be able to perform binary search on it
		idx := sort.Search(len(objectStores), func(i int) bool {
			return strings.Compare(objectStores[i].ResourceGroup, bucketInfo.Azure.ResourceGroup) >= 0 &&
				strings.Compare(objectStores[i].StorageAccount, bucketInfo.Azure.StorageAccount) >= 0 &&
				strings.Compare(objectStores[i].Name, bucketInfo.Name) >= 0
		})

		if idx < len(objectStores) &&
			strings.Compare(objectStores[idx].ResourceGroup, bucketInfo.Azure.ResourceGroup) >= 0 &&
			strings.Compare(objectStores[idx].StorageAccount, bucketInfo.Azure.StorageAccount) >= 0 &&
			strings.Compare(objectStores[idx].Name, bucketInfo.Name) >= 0 {
			bucketInfo.Managed = true
		}
	}

	return buckets, nil
}

func (s *ObjectStore) ListManagedBuckets() ([]*objectstore.BucketInfo, error) {

	s.logger.Info("getting all resource groups for subscription")

	var objectStores []ObjectStoreBucketModel
	err := s.db.
		Where(&ObjectStoreBucketModel{OrganizationID: s.org.ID}).
		Order("resource_group asc, storage_account asc, name asc").
		Find(&objectStores).Error

	if err != nil {
		return nil, fmt.Errorf("retrieving managed buckets failed: %s", err.Error())
	}

	bucketList := make([]*objectstore.BucketInfo, 0)
	for _, bucket := range objectStores {
		bucketInfo := &objectstore.BucketInfo{Name: bucket.Name, Managed: true}
		bucketInfo.Location = bucket.Location
		bucketInfo.SecretRef = bucket.SecretRef
		bucketInfo.Cloud = providers.Azure
		bucketInfo.Status = bucket.Status
		bucketInfo.StatusMsg = bucket.StatusMsg
		bucketInfo.Azure = &objectstore.BlobStoragePropsForAzure{
			ResourceGroup:  bucket.ResourceGroup,
			StorageAccount: bucket.StorageAccount,
		}
		bucketList = append(bucketList, bucketInfo)
	}

	return bucketList, nil
}

func GetStorageAccountKey(resourceGroup string, storageAccount string, s *secret.SecretItemResponse, log logrus.FieldLogger) (string, error) {
	client, err := createStorageAccountClient(s)
	if err != nil {
		return "", err
	}

	logger := log.WithFields(logrus.Fields{
		"resource_group":  resourceGroup,
		"storage_account": storageAccount,
	})

	logger.Info("getting key for storage account")

	keys, err := client.ListKeys(context.TODO(), resourceGroup, storageAccount)
	if err != nil {
		return "", errors.Wrap(err, "error retrieving keys for StorageAccount")
	}

	key := (*keys.Keys)[0].Value

	return *key, nil
}

func createStorageAccountClient(s *secret.SecretItemResponse) (*storage.AccountsClient, error) {
	accountClient := storage.NewAccountsClient(s.Values[pkgSecret.AzureSubscriptionId])

	authorizer, err := newAuthorizer(s)
	if err != nil {
		return nil, errors.Wrap(err, "error happened during authentication")
	}

	accountClient.Authorizer = authorizer

	return &accountClient, nil
}

// getAllResourceGroups returns all resource groups using
// the Azure credentials referenced by the provided secret.
func getAllResourceGroups(s *secret.SecretItemResponse) ([]*resources.Group, error) {
	rgClient := resources.NewGroupsClient(s.GetValue(pkgSecret.AzureSubscriptionId))
	authorizer, err := newAuthorizer(s)
	if err != nil {
		return nil, err
	}

	rgClient.Authorizer = authorizer

	resourceGroupsPages, err := rgClient.List(context.TODO(), "", nil)
	if err != nil {
		return nil, err
	}

	var groups []*resources.Group
	for resourceGroupsPages.NotDone() {
		resourceGroupsChunk := resourceGroupsPages.Values()

		for i := 0; i < len(resourceGroupsChunk); i++ {
			groups = append(groups, &resourceGroupsChunk[i])
		}

		if err = resourceGroupsPages.Next(); err != nil {
			return nil, err
		}
	}

	return groups, nil
}

// getAllStorageAccounts returns all storage accounts under the specified resource group
// using the Azure credentials referenced by the provided secret.
func getAllStorageAccounts(s *secret.SecretItemResponse, resourceGroup string) (*[]storage.Account, error) {
	client, err := createStorageAccountClient(s)
	if err != nil {
		return nil, err
	}

	storageAccountList, err := client.ListByResourceGroup(context.TODO(), resourceGroup)
	if err != nil {
		return nil, err
	}

	return storageAccountList.Value, nil
}

// getAllBlobContainers returns all blob container that belong to the specified storage account using
// the given storage account key.
func getAllBlobContainers(storageAccount string, storageAccountKey string) ([]azblob.Container, error) {
	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net", storageAccount))

	p := azblob.NewPipeline(azblob.NewSharedKeyCredential(storageAccount, storageAccountKey), azblob.PipelineOptions{})
	serviceURL := azblob.NewServiceURL(*u, p)

	resp, err := serviceURL.ListContainers(context.TODO(), azblob.Marker{}, azblob.ListContainersOptions{})
	if err != nil {
		return nil, err
	}

	return resp.Containers, nil
}

func newAuthorizer(s *secret.SecretItemResponse) (autorest.Authorizer, error) {
	authorizer, err := auth.NewClientCredentialsConfig(
		s.Values[pkgSecret.AzureClientId],
		s.Values[pkgSecret.AzureClientSecret],
		s.Values[pkgSecret.AzureTenantId]).Authorizer()

	if err != nil {
		return nil, err
	}

	return authorizer, nil
}

// searchCriteria returns the database search criteria to find a bucket with the given name
// within the scope of the specified resource group and storage account.
func (s *ObjectStore) searchCriteria(bucketName string) *ObjectStoreBucketModel {
	return &ObjectStoreBucketModel{
		OrganizationID: s.org.ID,
		Name:           bucketName,
		ResourceGroup:  s.getResourceGroup(),
		StorageAccount: s.getStorageAccount(),
	}
}
