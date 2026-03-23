// 1ES Managed DevOps Pool for Karpenter CI
//
// Deploys a 1ES hosted pool targeting GitHub Actions for long-running CI jobs
// (E2E tests, unit tests, deflake). Uses Standard_D8ds_v4 VMs (8 vCPU, 32 GB)
// with stateless (ephemeral) agents.
//
// Usage:
//   ./deploy.sh
//
// Prerequisites:
//   - az login
//   - AZURE_SUBSCRIPTION_ID env var set

param location string = resourceGroup().location

var poolName = 'karpenter-ci-1es-pool'
var sku = 'Standard_D8ds_v4'

// Base 1ES Image Resource IDs
var ubuntu2204GalleryVersionResourceId = '/subscriptions/723b64f0-884d-4994-b6de-8960d049cb7e/resourceGroups/CloudTestImages/providers/Microsoft.Compute/galleries/CloudTestGallery/images/MMSUbuntu22.04-Secure/versions/latest'

var poolSettings = {
  maxPoolSize: 8 // We run up to 14 E2E suites + CI tests, so a max of 8 allows good parallelism
  resourcePredictions: [
    {}              // Sunday: no agents
    {
      '17:00': 2   // 9 AM Monday PST
    }
    {
      '01:00': 0   // 5 PM Monday PST
      '17:00': 2   // 9 AM Tuesday PST
    }
    {
      '01:00': 0   // 5 PM Tuesday PST
      '17:00': 2   // 9 AM Wednesday PST
    }
    {
      '01:00': 0   // 5 PM Wednesday PST
      '17:00': 2   // 9 AM Thursday PST
    }
    {
      '01:00': 0   // 5 PM Thursday PST
      '17:00': 2   // 9 AM Friday PST
    }
    {
      '01:00': 0   // 5 PM Friday PST
    }
  ]
}

resource agentImage 'Microsoft.CloudTest/images@2020-05-07' = {
  name: '1es-ubuntu-22.04'
  location: location
  properties: {
    imageType: 'SharedImageGallery'
    resourceId: ubuntu2204GalleryVersionResourceId
  }
}

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
      tier: 'Standard'
    }
    images: [
      {
        subscriptionId: subscription().subscriptionId
        imageName: agentImage.name
        poolBufferPercentage: '100'
      }
    ]
    maxPoolSize: poolSettings.maxPoolSize
    agentProfile: {
      type: 'Stateless'
      resourcePredictions: poolSettings.resourcePredictions
    }
  }
}
