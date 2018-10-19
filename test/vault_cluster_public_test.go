package test

import (
	"fmt"
	"github.com/gruntwork-io/terratest/modules/gcp"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/ssh"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/gruntwork-io/terratest/modules/test-structure"
	"path/filepath"
	"strings"
	"testing"
)

// Terratest saved value names
const SAVED_GCP_PROJECT_ID = "GcpProjectId"
const SAVED_GCP_REGION_NAME = "GcpRegionName"
const SAVED_GCP_ZONE_NAME = "GcpZoneName"
const SAVED_CONSUL_CLUSTER_NAME = "ConsulClusterName"
const SAVED_VAULT_CLUSTER_NAME = "VaultClusterName"

// Terraform module vars
const TFVAR_NAME_GCP_PROJECT_ID = "gcp_project_id"
const TFVAR_NAME_GCP_REGION = "gcp_region"

const TFVAR_NAME_VAULT_CLUSTER_NAME = "vault_cluster_name"
const TFVAR_NAME_VAULT_SOURCE_IMAGE = "vault_source_image"
const TFVAR_NAME_VAULT_CLUSTER_MACHINE_TYPE = "vault_cluster_machine_type"

const TFVAR_NAME_CONSUL_SOURCE_IMAGE = "consul_server_source_image"
const TFVAR_NAME_CONSUL_SERVER_CLUSTER_NAME = "consul_server_cluster_name"
const TFVAR_NAME_CONSUL_SERVER_CLUSTER_MACHINE_TYPE = "consul_server_machine_type"

func TestIntegrationVaultOpenSourcePublicClusterUbuntu(t *testing.T) {
	t.Parallel()

	testVaultPublicCluster(t, "ubuntu-16")
}

func testVaultPublicCluster(t *testing.T, osName string) {
	exampleDir := test_structure.CopyTerraformFolderToTemp(t, "../", ".")
	vaultImageDir := filepath.Join(exampleDir, "examples", "vault-consul-image")
	vaultImagePath := filepath.Join(vaultImageDir, "vault-consul.json")

	test_structure.RunTestStage(t, "build_image", func() {
		projectId := gcp.GetGoogleProjectIDFromEnvVar(t)
		region := gcp.GetRandomRegion(t, projectId, nil, nil)
		zone := gcp.GetRandomZoneForRegion(t, projectId, region)

		test_structure.SaveString(t, exampleDir, SAVED_GCP_PROJECT_ID, projectId)
		test_structure.SaveString(t, exampleDir, SAVED_GCP_REGION_NAME, region)
		test_structure.SaveString(t, exampleDir, SAVED_GCP_ZONE_NAME, zone)

		tlsCert := generateSelfSignedTlsCert(t)
		saveTLSCert(t, vaultImageDir, tlsCert)

		imageID := buildVaultImage(t, vaultImagePath, osName, projectId, zone, tlsCert)
		test_structure.SaveArtifactID(t, exampleDir, imageID)
	})

	defer test_structure.RunTestStage(t, "teardown", func() {
		projectID := test_structure.LoadString(t, exampleDir, SAVED_GCP_PROJECT_ID)
		imageName := test_structure.LoadArtifactID(t, exampleDir)

		image := gcp.FetchImage(t, projectID, imageName)
		image.DeleteImage(t)

		tlsCert := loadTLSCert(t, vaultImageDir)
		cleanupTLSCertFiles(tlsCert)
	})

	test_structure.RunTestStage(t, "deploy", func() {
		projectId := test_structure.LoadString(t, exampleDir, SAVED_GCP_PROJECT_ID)
		region := test_structure.LoadString(t, exampleDir, SAVED_GCP_REGION_NAME)
		imageID := test_structure.LoadArtifactID(t, exampleDir)

		// GCP only supports lowercase names for some resources
		uniqueID := strings.ToLower(random.UniqueId())

		consulClusterName := fmt.Sprintf("consul-test-%s", uniqueID)
		vaultClusterName := fmt.Sprintf("vault-test-%s", uniqueID)

		test_structure.SaveString(t, exampleDir, SAVED_CONSUL_CLUSTER_NAME, consulClusterName)
		test_structure.SaveString(t, exampleDir, SAVED_VAULT_CLUSTER_NAME, vaultClusterName)

		terraformOptions := &terraform.Options{
			TerraformDir: exampleDir,
			Vars: map[string]interface{}{
				TFVAR_NAME_GCP_PROJECT_ID:                     projectId,
				TFVAR_NAME_GCP_REGION:                         region,
				TFVAR_NAME_CONSUL_SERVER_CLUSTER_NAME:         consulClusterName,
				TFVAR_NAME_CONSUL_SOURCE_IMAGE:                imageID,
				TFVAR_NAME_CONSUL_SERVER_CLUSTER_MACHINE_TYPE: "g1-small",
				TFVAR_NAME_VAULT_CLUSTER_NAME:                 vaultClusterName,
				TFVAR_NAME_VAULT_SOURCE_IMAGE:                 imageID,
				TFVAR_NAME_VAULT_CLUSTER_MACHINE_TYPE:         "g1-small",
			},
		}
		test_structure.SaveTerraformOptions(t, exampleDir, terraformOptions)

		terraform.InitAndApply(t, terraformOptions)
	})

	test_structure.RunTestStage(t, "validate", func() {
		projectId := test_structure.LoadString(t, exampleDir, SAVED_GCP_PROJECT_ID)
		region := test_structure.LoadString(t, exampleDir, SAVED_GCP_REGION_NAME)
		vaultClusterName := test_structure.LoadString(t, exampleDir, SAVED_VAULT_CLUSTER_NAME)

		sshUserName := "terratest"
		keyPair := ssh.GenerateRSAKeyPair(t, 2048)

		instanceGroup := gcp.FetchRegionalInstanceGroup(t, projectId, region, vaultClusterName)
		instances := instanceGroup.GetInstances(t, projectId)

		for _, instance := range instances {
			instance.AddSshKey(t, sshUserName, keyPair.PublicKey)
		}

		initializeAndUnsealVaultCluster(t, projectId, region, vaultClusterName, sshUserName, keyPair)
		testVault(t, instances[0].GetPublicIp(t))
	})
}