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

package models

const (
	OSType_Unspecified int32 = 0
	OSType_Windows     int32 = 1
	OSType_Linux       int32 = 2
)

const (
	OSSKU_Unspecified   int32 = 0
	OSSKU_Ubuntu        int32 = 1
	OSSKU_AzureLinux    int32 = 7
	OSSKU_Windows2019   int32 = 3
	OSSKU_Windows2022   int32 = 4
	OSSKU_WindowsAnnual int32 = 8
)

const (
	KubeletDiskType_Unspecified int32 = 0
	KubeletDiskType_OS          int32 = 1
	KubeletDiskType_Temporary   int32 = 2
)

const (
	AgentPoolMode_Unspecified int32 = 0
	AgentPoolMode_System      int32 = 1
	AgentPoolMode_User        int32 = 2
)

const (
	GPUInstanceProfile_Unspecified int32 = 0
)

const (
	WorkloadRuntime_Unspecified int32 = 0
)

const (
	SSHAccess_LocalUser int32 = 0
	SSHAccess_Disabled  int32 = 1
)

const (
	DriverType_Unspecified int32 = 0
	DriverType_GRID        int32 = 2
	DriverType_CUDA        int32 = 3
)
