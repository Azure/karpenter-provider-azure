package acr

import (
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	"sigs.k8s.io/karpenter/pkg/test"
)

var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *corev1beta1.NodePool


func TestAcr(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = azure.NewEnvironment(t)
	})
	RunSpecs(t, "Acr")
}

var _ = BeforeEach(func() { 
	env.BeforeEach() 
	nodeClass = env.DefaultAKSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })

var _ = Describe("Acr", func() {
	Describe("Image Pull", func(){
		It("should allow karpenter user pool nodes to pull images from the clusters attached acr", func(){
			acrName := os.Getenv("AZURE_ACR_NAME")
			deployment := test.Deployment(test.DeploymentOptions{
				Replicas: 10,
				PodOptions: test.PodOptions{
					ResourceRequirements: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource.MustParse("1.1"),
						},
					},
					Image: fmt.Sprintf("%s/pause:3.6",acrName),
				},
			})
			
			env.ExpectCreated(nodePool, nodeClass, deployment)
			env.EventuallyExpectHealthyPodCountWithTimeout(time.Minute*15, labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), int(*deployment.Spec.Replicas))
			env.ExpectCreatedNodeCount("==", int(*deployment.Spec.Replicas))

		})
	})
})
