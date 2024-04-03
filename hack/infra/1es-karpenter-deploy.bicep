// Base Vars
var location = 'westus3'

// Step 1: Create the 1es image
var imageName = '1es-aks-karpenter-image-ubuntu'

// Base 1ES Image Resource IDs
var ubuntu2204GalleryVersionResourceId = '/subscriptions/723b64f0-884d-4994-b6de-8960d049cb7e/resourceGroups/CloudTestImages/providers/Microsoft.Compute/galleries/CloudTestGallery/images/MMSUbuntu22.04-Secure/versions/latest'

resource agentImage 'Microsoft.CloudTest/images@2020-05-07' = {
  name: imageName
  location: location
  properties: {
    imageType: 'SharedImageGallery'
    resourceId: ubuntu2204GalleryVersionResourceId
  }
}

// Step 3: Create a user-defined managed identity
var msiName = 'aks-karpenter-acrpush-access'

resource msi 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: msiName
  location: 'westus3'
}

// Step 2: Create the 1es pool & Step 4: Update the pool to use that identity
var poolName = '1es-aks-karpenter-pool-ubuntu'
var sku = 'Standard_D2ads_v5'

resource hostedPool 'Microsoft.CloudTest/hostedpools@2020-05-07' = {
  name: poolName
  location: location
  properties: {
    organizationProfile: {
      type: 'GitHub'
      organizationName: 'Azure'
      url: 'https://github.com/Azure/karpenter-provider-azure'
    }
    sku: {
      name: sku
      tier: 'Standard' // Supports premium but we don't need it as work is done on temp disk anyway. See https://eng.ms/docs/cloud-ai-platform/devdiv/one-engineering-system-1es/1es-docs/1es-hosted-azure-devops-pools/demands.
    }
    images: [
      {
        subscriptionId: subscription().subscriptionId
        imageName: agentImage.name
        poolBufferPercentage: '*'
      }
    ]
    maxPoolSize: '3'
    agentProfile: {
      type: 'Stateless'
    }
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${msi.id}': {}
    }
  }
}

// Step 5: Give that identity the ACRPush role assignment on the ACR
var acrName = 'AKSMCRImagesCommon'
var acrResourceGroup = 'Repos4MCR'

// ACRPush
var roleDefinitionID = '8311e382-0749-4cb8-b61a-304f252e45ec'

module roleAssignment '1es-karpenter-acr-roleassignment.bicep' = {
  name: 'role-assignment-name'
  scope: resourceGroup(acrResourceGroup)
  params: {
    acrName: acrName
    principalId: msi.properties.principalId
    msiName: msi.name
    roleDefinitionID: roleDefinitionID
  }
}