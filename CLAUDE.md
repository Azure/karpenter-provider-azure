# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the Karpenter Provider for Azure (Azure Karpenter Provider), which enables node autoprovisioning using Karpenter on AKS clusters. It's a fork of the AWS Karpenter project adapted for Azure's infrastructure and services.

## Development Commands

### Essential Build and Test Commands
- `make test` - Run unit and acceptance tests using Ginkgo
- `make verify` - Comprehensive verification including linting, formatting, codegen, and validation
- `make presubmit` - Run both verify and test (complete developer loop)
- `make vulncheck` - Run security vulnerability checks
- `make codegen` - Generate code from Azure API responses

### E2E Testing
- `make e2etests TEST_SUITE=<suite>` - Run specific E2E test suite (integration, scheduling, etc.)
- `make az-e2etests` - Run E2E tests with Azure environment setup
- `make upstream-e2etests` - Run upstream Karpenter E2E tests

### Azure Development Workflow
- `make az-all` - Complete Azure setup: provision infra (ACR, AKS), build and deploy Karpenter
- `make az-build` - Build controller and webhook images using skaffold/ko
- `make az-run` - Deploy from current git state to cluster
- `make az-debug` - Build and deploy with debugging support (attach with VSCode F5)
- `make az-cleanup` - Delete deployment
- `make az-rmrg` - Delete entire resource group (use with care)

### Useful Development Commands
- `make toolchain` - Install required developer tools
- `make tidy` - Run "go mod tidy" recursively
- `make download` - Download dependencies recursively

## Architecture Overview

### Core Components
- **CloudProvider** (`pkg/cloudprovider/`) - Main interface between Karpenter core and Azure
- **Controllers** (`pkg/controllers/`) - Kubernetes controllers for node lifecycle management
  - `nodeclaim/` - Manages individual node provisioning and cleanup
  - `nodeclass/` - Manages AKSNodeClass resources (Azure-specific node configuration)
- **Providers** (`pkg/providers/`) - Azure service integrations
  - `instance/` - Azure VM management and SKU selection
  - `imagefamily/` - OS image selection and bootstrapping
  - `instancetype/` - VM size/type selection
  - `pricing/` - Azure pricing data integration

### Key APIs and CRDs
- **AKSNodeClass** (`pkg/apis/v1beta1/aksnodeclass.go`) - Azure-specific node configuration
- **NodePool** - Standard Karpenter resource for node grouping
- **NodeClaim** - Standard Karpenter resource for individual nodes

### Testing Structure
- **Unit Tests** - Located alongside source code in `pkg/`
- **Acceptance Tests** - Integration tests with faked Azure clients
- **E2E Tests** - Full integration tests in `test/suites/` against real AKS clusters

### Configuration and Authentication
- **Auth** (`pkg/auth/`) - Azure authentication using workload identity or service principals
- **Settings** (`pkg/apis/settings/`) - Cluster-specific configuration
- **Options** (`pkg/operator/options/`) - Operator configuration and validation

## Important Development Notes

### Environment Setup
The project supports multiple deployment modes:
- **NAP (Node Auto Provisioning)** - Managed addon mode (recommended for users)
- **Self-hosted** - Standalone deployment (development and advanced users)

### Azure Resource Management
- Uses Azure SDK v2 with ARM (Azure Resource Manager)
- Integrates with AKS node bootstrapping via provision clients
- Manages VM Scale Sets, Network Interfaces, and Storage resources

### Testing Philosophy
- Acceptance tests fake Azure API calls for fast feedback
- E2E tests run against real AKS clusters with actual Azure resources
- Use Ginkgo testing framework throughout

### Code Generation
- `make verify` runs codegen and validates that generated files are up-to-date
- Swagger client generation for node bootstrapping APIs
- CRD generation and validation

### Key Files to Understand
- `Makefile` + `Makefile-az.mk` - Build and deployment automation
- `skaffold.yaml` - Local development deployment configuration
- `cmd/controller/main.go` - Application entry point
- `pkg/cloudprovider/cloudprovider.go` - Primary CloudProvider implementation

When working on this codebase, always run `make verify` before submitting changes to ensure code generation, linting, and formatting are correct.
