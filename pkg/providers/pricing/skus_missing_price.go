// Portions Copyright (c) Microsoft Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pricing

// setting to a high value means it won't be chosen automatically,
// but will still be available if chosen explicitly
const missingPrice float64 = 999.0

// skusWithMissingPrice is a map of SKUs that are known to have missing prices in the Azure pricing API.
var skusWithMissingPrice = map[string]float64{
	"Standard_A2":               missingPrice,
	"Standard_A3":               missingPrice,
	"Standard_A4":               missingPrice,
	"Standard_A5":               missingPrice,
	"Standard_A6":               missingPrice,
	"Standard_A7":               missingPrice,
	"Standard_D1":               missingPrice,
	"Standard_D11":              missingPrice,
	"Standard_D11_v2":           missingPrice,
	"Standard_D11_v2_Promo":     missingPrice,
	"Standard_D12":              missingPrice,
	"Standard_D12_v2":           missingPrice,
	"Standard_D12_v2_Promo":     missingPrice,
	"Standard_D13":              missingPrice,
	"Standard_D13_v2":           missingPrice,
	"Standard_D13_v2_Promo":     missingPrice,
	"Standard_D14":              missingPrice,
	"Standard_D14_v2":           missingPrice,
	"Standard_D14_v2_Promo":     missingPrice,
	"Standard_D15_v2":           missingPrice,
	"Standard_D1_v2":            missingPrice,
	"Standard_D2":               missingPrice,
	"Standard_D2_v2":            missingPrice,
	"Standard_D2_v2_Promo":      missingPrice,
	"Standard_D3":               missingPrice,
	"Standard_D3_v2":            missingPrice,
	"Standard_D3_v2_Promo":      missingPrice,
	"Standard_D4":               missingPrice,
	"Standard_D4_v2":            missingPrice,
	"Standard_D4_v2_Promo":      missingPrice,
	"Standard_D5_v2":            missingPrice,
	"Standard_D5_v2_Promo":      missingPrice,
	"Standard_DS1":              missingPrice,
	"Standard_DS11":             missingPrice,
	"Standard_DS11-1_v2":        missingPrice,
	"Standard_DS11_v2":          missingPrice,
	"Standard_DS11_v2_Promo":    missingPrice,
	"Standard_DS12":             missingPrice,
	"Standard_DS12-1_v2":        missingPrice,
	"Standard_DS12-2_v2":        missingPrice,
	"Standard_DS12_v2":          missingPrice,
	"Standard_DS12_v2_Promo":    missingPrice,
	"Standard_DS13":             missingPrice,
	"Standard_DS13-2_v2":        missingPrice,
	"Standard_DS13-4_v2":        missingPrice,
	"Standard_DS13_v2":          missingPrice,
	"Standard_DS13_v2_Promo":    missingPrice,
	"Standard_DS14":             missingPrice,
	"Standard_DS14-4_v2":        missingPrice,
	"Standard_DS14-8_v2":        missingPrice,
	"Standard_DS14_v2":          missingPrice,
	"Standard_DS14_v2_Promo":    missingPrice,
	"Standard_DS15_v2":          missingPrice,
	"Standard_DS1_v2":           missingPrice,
	"Standard_DS2":              missingPrice,
	"Standard_DS2_v2":           missingPrice,
	"Standard_DS2_v2_Promo":     missingPrice,
	"Standard_DS3":              missingPrice,
	"Standard_DS3_v2":           missingPrice,
	"Standard_DS3_v2_Promo":     missingPrice,
	"Standard_DS4":              missingPrice,
	"Standard_DS4_v2":           missingPrice,
	"Standard_DS4_v2_Promo":     missingPrice,
	"Standard_DS5_v2":           missingPrice,
	"Standard_DS5_v2_Promo":     missingPrice,
	"Standard_E96-24ads_v6":     missingPrice,
	"Standard_E96-48ads_v6":     missingPrice,
	"Standard_EC128ieds_v5":     missingPrice,
	"Standard_EC128ies_v5":      missingPrice,
	"Standard_L16s":             missingPrice,
	"Standard_L32s":             missingPrice,
	"Standard_L4s":              missingPrice,
	"Standard_L8s":              missingPrice,
	"Standard_M416s_10_v2":      missingPrice,
	"Standard_M416s_9_v2":       missingPrice,
	"Standard_ND40s_v3":         missingPrice,
	"Standard_NG32adms_V620_v1": missingPrice,
}
