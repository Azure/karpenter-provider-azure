// 1ES Managed DevOps Pool for Karpenter CI
//
// Deploys a 1ES hosted pool targeting GitHub Actions for long-running CI jobs
// (E2E tests, unit tests, deflake). Uses Standard_D4ds_v5 VMs (4 vCPU, 16 GB)
// with stateless (ephemeral) agents.
//
// Usage:
//   ./deploy.sh
//
// Prerequisites:
//   - az login
//   - AZURE_SUBSCRIPTION_ID env var set

param location string = 'westus3'

var poolName = 'karpenter-ci-1es-pool'
var sku = 'Standard_D4ds_v5'

// Base 1ES Image Resource IDs
var ubuntu2204GalleryVersionResourceId = '/subscriptions/723b64f0-884d-4994-b6de-8960d049cb7e/resourceGroups/CloudTestImages/providers/Microsoft.Compute/galleries/CloudTestGallery/images/MMSUbuntu22.04-Secure/versions/latest'

var poolSettings = {
  maxPoolSize: 25 // 25 × 4 = 100 cores (DDSv5 family)
  resourcePredictions: [
    {
      '21:00': 12  // 9 AM Monday NZT
    }
    {
      '05:00': 0   // 5 PM Monday NZT
      '16:00': 12  // 9 AM Monday PST
    }
    {
      '05:00': 0   // 5 PM Tuesday NZT
      '16:00': 12  // 9 AM Tuesday PST
    }
    {
      '05:00': 0   // 5 PM Wednesday NZT
      '16:00': 12  // 9 AM Wednesday PST
    }
    {
      '05:00': 0   // 5 PM Thursday NZT
      '16:00': 12  // 9 AM Thursday PST
    }
    {
      '05:00': 0   // 5 PM Friday NZT
      '16:00': 12  // 9 AM Friday PST
    }
    {
      '00:00': 0   // 5 PM Friday PST
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
