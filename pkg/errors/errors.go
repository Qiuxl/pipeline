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

package errors

import "errors"

// ### [ Errors ] ### //
var (
	ErrorNotSupportedCloudType    = errors.New("Not supported cloud type")
	ErrorAmazonImageFieldIsEmpty  = errors.New("Required field 'image' is empty ")
	ErrorInstancetypeFieldIsEmpty = errors.New("Required field 'instanceType' is empty ")

	ErrorAmazonEksClusterNameRegexp        = errors.New("Up to 255 letters (uppercase and lowercase), numbers, hyphens, and underscores are allowed.")
	ErrorAmazonEksFieldIsEmpty             = errors.New("Required field 'eks' is empty.")
	ErrorAmazonEksMasterFieldIsEmpty       = errors.New("Required field 'master' is empty.")
	ErrorAmazonEksImageFieldIsEmpty        = errors.New("Required field 'image' is empty ")
	ErrorAmazonEksNodePoolFieldIsEmpty     = errors.New("At least one 'nodePool' is required.")
	ErrorAmazonEksInstancetypeFieldIsEmpty = errors.New("Required field 'instanceType' is empty ")

	ErrorNodePoolMinMaxFieldError     = errors.New("'maxCount' must be greater than 'minCount'")
	ErrorNodePoolCountFieldError      = errors.New("'count' must be greater than or equal to 'minCount' and lower than or equal to 'maxCount'")
	ErrorMinFieldRequiredError        = errors.New("'minCount' must be set in case 'autoscaling' is set to true")
	ErrorMaxFieldRequiredError        = errors.New("'maxCount' must be set in case 'autoscaling' is set to true")
	ErrorGoogleClusterNameRegexp      = errors.New("Name must start with a lowercase letter followed by up to 40 lowercase letters, numbers, or hyphens, and cannot end with a hyphen.")
	ErrorAzureClusterNameRegexp       = errors.New("Only numbers, lowercase letters and underscores are allowed under name property. In addition, the value cannot end with an underscore, and must also be less than 32 characters long.")
	ErrorAzureClusterNameEmpty        = errors.New("The name should not be empty.")
	ErrorAzureClusterNameTooLong      = errors.New("Cluster name is greater than or equal 32")
	ErrorAzureCLusterStageFailed      = errors.New("cluster stage is 'Failed'")
	ErrorAzureFieldIsEmpty            = errors.New("Azure is <nil>")
	ErrorNodePoolEmpty                = errors.New("Required field 'nodePools' is empty.")
	ErrorNotDifferentInterfaces       = errors.New("There is no change in data")
	ErrorReconcile                    = errors.New("Error during reconcile")
	ErrorEmptyUpdateRequest           = errors.New("Empty update cluster request")
	ErrorClusterNotReady              = errors.New("Cluster not ready yet")
	ErrorNilCluster                   = errors.New("<nil> cluster")
	ErrorWrongKubernetesVersion       = errors.New("Wrong kubernetes version for master/nodes. The required minimum kubernetes version is 1.8.x ")
	ErrorDifferentKubernetesVersion   = errors.New("Different kubernetes version for master and nodes")
	ErrorLocationEmpty                = errors.New("Location field is empty")
	ErrorNodeInstanceTypeEmpty        = errors.New("instanceType field is empty")
	ErrorRequiredLocation             = errors.New("location is required")
	ErrorRequiredSecretId             = errors.New("Secret id is required")
	ErrorCloudInfoK8SNotSupported     = errors.New("Not supported key in case of amazon")
	ErrorNodePoolNotProvided          = errors.New("At least one 'nodepool' is required for creating or updating a cluster")
	ErrorOnlyOneNodeModify            = errors.New("only one node can be modified at a time")
	ErrorNotValidLocation             = errors.New("not valid location")
	ErrorNotValidMasterImage          = errors.New("not valid master image")
	ErrorNotValidNodeImage            = errors.New("not valid node image")
	ErrorNotValidNodeInstanceType     = errors.New("not valid nodeInstanceType")
	ErrorNotValidMasterVersion        = errors.New("not valid master version")
	ErrorNotValidNodeVersion          = errors.New("not valid node version")
	ErrorNotValidKubernetesVersion    = errors.New("not valid kubernetesVersion")
	ErrorResourceGroupRequired        = errors.New("resource group is required")
	ErrorProjectRequired              = errors.New("project is required")
	ErrorNodePoolNotFoundByName       = errors.New("nodepool not found by name")
	ErrorNoInfrastructureRG           = errors.New("no infrastructure resource group found")
	ErrStateStorePathEmpty            = errors.New("statestore path cannot be empty")
	ErrorAlibabaFieldIsEmpty          = errors.New("Required field 'alibaba' is empty.")
	ErrorAlibabaRegionIDFieldIsEmpty  = errors.New("Required field 'region_id' is empty.")
	ErrorAlibabaZoneIDFieldIsEmpty    = errors.New("Required field 'zoneid' is empty.")
	ErrorAlibabaNodePoolFieldIsEmpty  = errors.New("At least one 'nodePool' is required.")
	ErrorAlibabaNodePoolFieldLenError = errors.New("Only one 'nodePool' is supported.")
	ErrorAlibabaMinNumberOfNodes      = errors.New("'num_of_nodes' must be greater than zero.")
)
