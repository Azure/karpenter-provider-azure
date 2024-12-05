package networking_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Azure/karpenter-provider-azure/pkg/apis/v1alpha2"
	"github.com/Azure/karpenter-provider-azure/test/pkg/environment/azure"
	corev1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"
	//"sigs.k8s.io/karpenter/pkg/test"
)



var env *azure.Environment
var nodeClass *v1alpha2.AKSNodeClass
var nodePool *corev1beta1.NodePool
var ns string
func TestNetworking(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func(){
		env = azure.NewEnvironment(t)
		ns = "default"	
	})
	AfterSuite(func() {
		By("Cleaning up Goldpinger resources")
		env.ExpectDeleted(
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger-serviceaccount", Namespace: ns}},
			&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger-clusterrole"}},
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger-clusterrolebinding"}},
			&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger-daemon", Namespace: ns}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger", Namespace: ns}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "goldpinger-deploy", Namespace: ns}},
		)
	})

	RunSpecs(t, "Networking")
}

var _ = BeforeEach(func() { env.BeforeEach() })
var _ = AfterEach(func() { env.Cleanup() })
var _ = AfterEach(func() { env.AfterEach() })


var _ = Describe("Networking", func() {
	Describe("GoldPinger", func(){
	It("should ensure goldpinger resources are all deployed", func() {
		By("Waiting for Goldpinger pods to be ready")
		serviceAccount := createServiceAccount(ns)
		clusterRole := createClusterRole()
		clusterRoleBinding := createClusterRoleBinding(ns)
		daemonSet := createDaemonSet(ns)
		service := createService(ns)
		deployment := createDeployment(ns)
	
		nodeClass := env.DefaultAKSNodeClass()
		nodePool := env.DefaultNodePool(nodeClass)
		By("Creating Goldpinger resources")
		env.ExpectCreated(serviceAccount, clusterRole, clusterRoleBinding, daemonSet, service, deployment, nodePool)
		Eventually(func() int {
			pods := &corev1.PodList{}
			err := env.Client.List(context.TODO(), pods, client.MatchingLabels{"app": "goldpinger"})
			Expect(err).NotTo(HaveOccurred(), "Failed to list Goldpinger pods")
			readyCount := 0
			for _, pod := range pods.Items {
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						readyCount++
					}
				}
			}
			return readyCount
		}, 5*time.Minute, 10*time.Second).Should(BeNumerically(">=", 10), "Not all Goldpinger pods are ready")
		Eventually(func() string {
			svc := &corev1.Service{}
			err := env.Client.Get(context.TODO(), client.ObjectKey{Name: "goldpinger", Namespace: ns}, svc)
			if err != nil {
				return ""
			}
			return svc.Spec.ClusterIP
	}, 2*time.Minute, 10*time.Second).ShouldNot(BeEmpty(), "Goldpinger service ClusterIP not assigned")
	})

	It("should verify node-to-node connectivity", func() {
		By("Fetching node connectivity status from Goldpinger")
		resp, err := http.Get("http://goldpinger.default.svc.cluster.local:8080/check_all")
		Expect(err).NotTo(HaveOccurred(), "Failed to reach Goldpinger service")
		defer resp.Body.Close()
					
		body, err := ioutil.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred(), "Failed to read Goldpinger response body")

		var checkAllResponse CheckAllResponse
		err = json.Unmarshal(body, &checkAllResponse)
		Expect(err).NotTo(HaveOccurred(), "Failed to parse Goldpinger response JSON")

		for node, status := range checkAllResponse.Nodes {
			Expect(status.Status).To(Equal("ok"), fmt.Sprintf("Node %s is not reachable", node))
		}
		time.Sleep(time.Hour * 1)
	})		
	})
	
})


// --------------------- Test Helpers ------------------------ //
type NodeStatus struct {
	Status  string `json:"status"`
	Latency int    `json:"latency"`
}

type CheckAllResponse struct {
	Nodes map[string]NodeStatus `json:"nodes"`
}

func createServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "goldpinger-serviceaccount",
			Namespace: namespace,
		},
	}
}

func createClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "goldpinger-clusterrole",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods", "nodes", "daemonsets"},
				Verbs:     []string{"list", "get", "watch"},
			},
		},
	}
}

func createClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "goldpinger-clusterrolebinding",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "goldpinger-serviceaccount",
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "goldpinger-clusterrole",
		},
	}
}

func createDaemonSet(namespace string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "goldpinger-daemon",
			Namespace: namespace,
			Labels:    map[string]string{"app": "goldpinger"},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "goldpinger"},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "goldpinger"},
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8080",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "goldpinger-serviceaccount",
					HostNetwork:        true,
					Containers: []corev1.Container{
						{
							Name:  "goldpinger",
							Image: "docker.io/bloomberg/goldpinger:v3.0.0",
							Env: []corev1.EnvVar{
								{Name: "USE_HOST_IP", Value: "true"},
								{Name: "HOST", Value: "0.0.0.0"},
								{Name: "PORT", Value: "8080"},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080, Name: "http"},
							},
						},
					},
				},
			},
		},
	}
}


func createService(namespace string) *corev1.Service {
    return &corev1.Service{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "goldpinger",
            Namespace: namespace,
            Labels:    map[string]string{"app": "goldpinger"},
        },
        Spec: corev1.ServiceSpec{
            Type: corev1.ServiceTypeNodePort,
            Ports: []corev1.ServicePort{
                {
                    Port:       8080,
                    TargetPort: intstr.FromInt(8080),
                    Name:       "http",
                },
            },
            Selector: map[string]string{"app": "goldpinger"},
        },
    }
}

func createDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "goldpinger-deploy",
			Namespace: namespace,
			Labels:    map[string]string{"app": "goldpinger"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](10),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "goldpinger"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "goldpinger"},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "goldpinger-serviceaccount",
					Containers: []corev1.Container{
						{
							Name:  "goldpinger",
							Image: "docker.io/bloomberg/goldpinger:v3.0.0",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080, Name: "http"},
							},
						},
					},
				},
			},
		},
	}
}

