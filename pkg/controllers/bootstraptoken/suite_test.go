/*
Portions Copyright (c) Microsoft Corporation.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bootstraptoken_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	bootstraptokencontroller "github.com/Azure/karpenter-provider-azure/pkg/controllers/bootstraptoken"
	"github.com/Azure/karpenter-provider-azure/pkg/operator/options"
	"github.com/Azure/karpenter-provider-azure/pkg/test"
	. "sigs.k8s.io/karpenter/pkg/test/expectations"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

var ctx context.Context
var opts *options.Options
var fakeKubeClient *fake.Clientset
var controller *bootstraptokencontroller.Controller

func TestController(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "BootstrapTokenController")
}

var _ = BeforeEach(func() {
	opts = test.Options(test.OptionsFields{
		KubeletClientTLSBootstrapToken: ptrTo(""),
	})
	fakeKubeClient = fake.NewSimpleClientset()
	controller = bootstraptokencontroller.NewController(fakeKubeClient, opts)
})

var _ = Describe("BootstrapToken Controller", func() {
	It("should return a requeue interval of 1 hour", func() {
		createBootstrapTokenSecret(fakeKubeClient, "token-id-val", "token-secret-val")
		result := ExpectSingletonReconciled(ctx, controller)
		Expect(result.RequeueAfter).To(Equal(bootstraptokencontroller.BootstrapTokenRefreshInterval))
	})

	It("should read and set the bootstrap token from kube-system secrets", func() {
		createBootstrapTokenSecret(fakeKubeClient, "abcdef", "0123456789abcdef")
		ExpectSingletonReconciled(ctx, controller)
		Expect(opts.KubeletClientTLSBootstrapToken).To(Equal("abcdef.0123456789abcdef"))
	})

	It("should fail when no bootstrap token secrets exist", func() {
		err := ExpectSingletonReconcileFailed(ctx, controller)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no bootstrap token secrets found"))
	})

	It("should fail when bootstrap token secret is missing token-id field", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap-token-test",
				Namespace: "kube-system",
			},
			Type: corev1.SecretType("bootstrap.kubernetes.io/token"),
			Data: map[string][]byte{
				"token-secret": []byte("0123456789abcdef"),
			},
		}
		_, err := fakeKubeClient.CoreV1().Secrets("kube-system").Create(ctx, secret, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		reconcileErr := ExpectSingletonReconcileFailed(ctx, controller)
		Expect(reconcileErr).To(HaveOccurred())
		Expect(reconcileErr.Error()).To(ContainSubstring("missing token-id field"))
	})

	It("should fail when bootstrap token secret is missing token-secret field", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap-token-test",
				Namespace: "kube-system",
			},
			Type: corev1.SecretType("bootstrap.kubernetes.io/token"),
			Data: map[string][]byte{
				"token-id": []byte("abcdef"),
			},
		}
		_, err := fakeKubeClient.CoreV1().Secrets("kube-system").Create(ctx, secret, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		reconcileErr := ExpectSingletonReconcileFailed(ctx, controller)
		Expect(reconcileErr).To(HaveOccurred())
		Expect(reconcileErr.Error()).To(ContainSubstring("missing token-secret field"))
	})

	It("should update the token when it changes on subsequent reconciliations", func() {
		createBootstrapTokenSecret(fakeKubeClient, "first-id", "first-secret")
		ExpectSingletonReconciled(ctx, controller)
		Expect(opts.KubeletClientTLSBootstrapToken).To(Equal("first-id.first-secret"))

		// Delete the old secret and create a new one
		err := fakeKubeClient.CoreV1().Secrets("kube-system").Delete(ctx, "bootstrap-token-test", metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
		createBootstrapTokenSecret(fakeKubeClient, "second-id", "second-secret")

		ExpectSingletonReconciled(ctx, controller)
		Expect(opts.KubeletClientTLSBootstrapToken).To(Equal("second-id.second-secret"))
	})
})

func createBootstrapTokenSecret(kubeClient *fake.Clientset, tokenID, tokenSecret string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-token-test",
			Namespace: "kube-system",
		},
		Type: corev1.SecretType("bootstrap.kubernetes.io/token"),
		Data: map[string][]byte{
			"token-id":     []byte(tokenID),
			"token-secret": []byte(tokenSecret),
		},
	}
	_, err := kubeClient.CoreV1().Secrets("kube-system").Create(context.Background(), secret, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())
}

func ptrTo[T any](v T) *T {
	return &v
}
