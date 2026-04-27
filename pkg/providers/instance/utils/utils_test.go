package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/karpenter-provider-azure/pkg/consts"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils Suite")
}

var _ = Describe("Utils", func() {
	Describe("HandleProvisioningState", func() {
		var (
			ctx        context.Context
			aksMachine *armcontainerservice.Machine
		)

		BeforeEach(func() {
			ctx = context.Background()
			aksMachine = &armcontainerservice.Machine{
				ID:   lo.ToPtr("test-machine-id"),
				Name: lo.ToPtr("test-machine-name"),
				Properties: &armcontainerservice.MachineProperties{
					ProvisioningState: lo.ToPtr(consts.ProvisioningStateCreating),
				},
			}
		})

		Context("non-terminal states", func() {
			It("should return done=false for Creating state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateCreating)

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeFalse())
			})

			It("should return done=false for Updating state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateUpdating)

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeFalse())
			})
		})

		Context("terminal success state", func() {
			It("should return done=true for Succeeded state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateSucceeded)

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeTrue())
			})
		})

		Context("terminal canceled state", func() {
			It("should return error for Deleting state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateDeleting)

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(HaveOccurred())
				Expect(pollingErr.Error()).To(ContainSubstring("canceled provisioning state"))
				Expect(pollingErr.Error()).To(ContainSubstring(consts.ProvisioningStateDeleting))
				Expect(done).To(BeTrue())
			})
		})

		Context("terminal failed state", func() {
			It("should return error details when ProvisioningError is present", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateFailed)
				aksMachine.Properties.Status = &armcontainerservice.MachineStatus{
					ProvisioningError: &armcontainerservice.ErrorDetail{
						Code:    lo.ToPtr("QuotaExceeded"),
						Message: lo.ToPtr("Quota exceeded for VM family"),
					},
				}

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).ToNot(BeNil())
				Expect(errorDetails.Code).To(Equal(lo.ToPtr("QuotaExceeded")))
				Expect(errorDetails.Message).To(Equal(lo.ToPtr("Quota exceeded for VM family")))
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeTrue())
			})

			It("should return error when ProvisioningError is nil", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateFailed)
				aksMachine.Properties.Status = &armcontainerservice.MachineStatus{
					ProvisioningError: nil,
				}

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(HaveOccurred())
				Expect(pollingErr.Error()).To(ContainSubstring("ProvisioningError is nil"))
				Expect(pollingErr.Error()).To(ContainSubstring(consts.ProvisioningStateFailed))
				Expect(done).To(BeTrue())
			})

			It("should return error when Status is nil", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr(consts.ProvisioningStateFailed)
				aksMachine.Properties.Status = nil

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(HaveOccurred())
				Expect(pollingErr.Error()).To(ContainSubstring("ProvisioningError is nil"))
				Expect(done).To(BeTrue())
			})
		})

		Context("unrecognized states", func() {
			It("should return done=false for unknown provisioning state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr("UnknownState")

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeFalse())
			})

			It("should return done=false for empty provisioning state", func() {
				aksMachine.Properties.ProvisioningState = lo.ToPtr("")

				errorDetails, pollingErr, done := HandleProvisioningState(ctx, aksMachine)

				Expect(errorDetails).To(BeNil())
				Expect(pollingErr).To(BeNil())
				Expect(done).To(BeFalse())
			})
		})
	})

	Describe("IsAKSMachineOrMachinesPoolNotFound", func() {
		Context("nil or no error", func() {
			It("should return false for nil error", func() {
				result := IsAKSMachineOrMachinesPoolNotFound(nil)

				Expect(result).To(BeFalse())
			})
		})

		Context("404 Not Found errors", func() {
			It("should return true for 404 StatusNotFound", func() {
				err := createAzureResponseError("NotFound", "Resource not found", http.StatusNotFound)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeTrue())
			})
		})

		Context("400 Bad Request with specific message", func() {
			It("should return true for InvalidParameter with 'Cannot find any valid machines' message", func() {
				err := createAzureResponseError("InvalidParameter", "Cannot find any valid machines in the pool", http.StatusBadRequest)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeTrue())
			})

			It("should return false for InvalidParameter with different message", func() {
				err := createAzureResponseError("InvalidParameter", "Some other validation error", http.StatusBadRequest)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})

			It("should return false for BadRequest with different error code", func() {
				err := createAzureResponseError("ValidationError", "Cannot find any valid machines", http.StatusBadRequest)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})
		})

		Context("other error codes", func() {
			It("should return false for 500 Internal Server Error", func() {
				err := createAzureResponseError("InternalServerError", "Server error", http.StatusInternalServerError)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})

			It("should return false for 403 Forbidden", func() {
				err := createAzureResponseError("Forbidden", "Access denied", http.StatusForbidden)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})

			It("should return false for 401 Unauthorized", func() {
				err := createAzureResponseError("Unauthorized", "Authentication required", http.StatusUnauthorized)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})

			It("should return false for 429 Too Many Requests", func() {
				err := createAzureResponseError("TooManyRequests", "Rate limited", http.StatusTooManyRequests)

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})
		})

		Context("non-Azure errors", func() {
			It("should return false for standard Go error", func() {
				err := fmt.Errorf("standard error")

				result := IsAKSMachineOrMachinesPoolNotFound(err)

				Expect(result).To(BeFalse())
			})
		})
	})
})

// createAzureResponseError creates an Azure ResponseError for testing purposes
func createAzureResponseError(errorCode, errorMessage string, statusCode int) error {
	errorBody := fmt.Sprintf(`{"error": {"code": "%s", "message": "%s"}}`, errorCode, errorMessage)
	return &azcore.ResponseError{
		ErrorCode:  errorCode,
		StatusCode: statusCode,
		RawResponse: &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(errorBody)),
		},
	}
}
