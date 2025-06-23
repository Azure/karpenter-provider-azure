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
const MissingPrice float64 = 999.0

// skusWithMissingPrice is a map of SKUs that are known to have missing prices in the Azure pricing API.
var skusWithMissingPrice = map[string]float64{
	"Standard_A2":               MissingPrice,
	"Standard_A3":               MissingPrice,
	"Standard_A4":               MissingPrice,
	"Standard_A5":               MissingPrice,
	"Standard_A6":               MissingPrice,
	"Standard_A7":               MissingPrice,
	"Standard_D1":               MissingPrice,
	"Standard_D11":              MissingPrice,
	"Standard_D11_v2":           MissingPrice,
	"Standard_D11_v2_Promo":     MissingPrice,
	"Standard_D12":              MissingPrice,
	"Standard_D12_v2":           MissingPrice,
	"Standard_D12_v2_Promo":     MissingPrice,
	"Standard_D13":              MissingPrice,
	"Standard_D13_v2":           MissingPrice,
	"Standard_D13_v2_Promo":     MissingPrice,
	"Standard_D14":              MissingPrice,
	"Standard_D14_v2":           MissingPrice,
	"Standard_D14_v2_Promo":     MissingPrice,
	"Standard_D15_v2":           MissingPrice,
	"Standard_D1_v2":            MissingPrice,
	"Standard_D2":               MissingPrice,
	"Standard_D2_v2":            MissingPrice,
	"Standard_D2_v2_Promo":      MissingPrice,
	"Standard_D3":               MissingPrice,
	"Standard_D3_v2":            MissingPrice,
	"Standard_D3_v2_Promo":      MissingPrice,
	"Standard_D4":               MissingPrice,
	"Standard_D4_v2":            MissingPrice,
	"Standard_D4_v2_Promo":      MissingPrice,
	"Standard_D5_v2":            MissingPrice,
	"Standard_D5_v2_Promo":      MissingPrice,
	"Standard_DS1":              MissingPrice,
	"Standard_DS11":             MissingPrice,
	"Standard_DS11-1_v2":        MissingPrice,
	"Standard_DS11_v2":          MissingPrice,
	"Standard_DS11_v2_Promo":    MissingPrice,
	"Standard_DS12":             MissingPrice,
	"Standard_DS12-1_v2":        MissingPrice,
	"Standard_DS12-2_v2":        MissingPrice,
	"Standard_DS12_v2":          MissingPrice,
	"Standard_DS12_v2_Promo":    MissingPrice,
	"Standard_DS13":             MissingPrice,
	"Standard_DS13-2_v2":        MissingPrice,
	"Standard_DS13-4_v2":        MissingPrice,
	"Standard_DS13_v2":          MissingPrice,
	"Standard_DS13_v2_Promo":    MissingPrice,
	"Standard_DS14":             MissingPrice,
	"Standard_DS14-4_v2":        MissingPrice,
	"Standard_DS14-8_v2":        MissingPrice,
	"Standard_DS14_v2":          MissingPrice,
	"Standard_DS14_v2_Promo":    MissingPrice,
	"Standard_DS15_v2":          MissingPrice,
	"Standard_DS1_v2":           MissingPrice,
	"Standard_DS2":              MissingPrice,
	"Standard_DS2_v2":           MissingPrice,
	"Standard_DS2_v2_Promo":     MissingPrice,
	"Standard_DS3":              MissingPrice,
	"Standard_DS3_v2":           MissingPrice,
	"Standard_DS3_v2_Promo":     MissingPrice,
	"Standard_DS4":              MissingPrice,
	"Standard_DS4_v2":           MissingPrice,
	"Standard_DS4_v2_Promo":     MissingPrice,
	"Standard_DS5_v2":           MissingPrice,
	"Standard_DS5_v2_Promo":     MissingPrice,
	"Standard_E96-24ads_v6":     MissingPrice,
	"Standard_E96-48ads_v6":     MissingPrice,
	"Standard_EC128ieds_v5":     MissingPrice,
	"Standard_EC128ies_v5":      MissingPrice,
	"Standard_L16s":             MissingPrice,
	"Standard_L32s":             MissingPrice,
	"Standard_L4s":              MissingPrice,
	"Standard_L8s":              MissingPrice,
	"Standard_M416s_10_v2":      MissingPrice,
	"Standard_M416s_9_v2":       MissingPrice,
	"Standard_ND40s_v3":         MissingPrice,
	"Standard_NG32adms_V620_v1": MissingPrice,
}
