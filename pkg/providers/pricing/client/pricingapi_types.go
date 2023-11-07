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

// Generated structs based off of the json retrieved from the https://prices.azure.com/api/retail/prices API

package client

import (
	"time"
)

type Item struct {
	CurrencyCode         string    `json:"currencyCode"`
	TierMinimumUnits     float64   `json:"tierMinimumUnits"`
	RetailPrice          float64   `json:"retailPrice"`
	UnitPrice            float64   `json:"unitPrice"`
	ArmRegionName        string    `json:"armRegionName"`
	Location             string    `json:"location"`
	EffectiveStartDate   time.Time `json:"effectiveStartDate"`
	MeterID              string    `json:"meterId"`
	MeterName            string    `json:"meterName"`
	ProductID            string    `json:"productId"`
	SkuID                string    `json:"skuId"`
	AvailabilityID       any       `json:"availabilityId"`
	ProductName          string    `json:"productName"`
	SkuName              string    `json:"skuName"`
	ServiceName          string    `json:"serviceName"`
	ServiceID            string    `json:"serviceId"`
	ServiceFamily        string    `json:"serviceFamily"`
	UnitOfMeasure        string    `json:"unitOfMeasure"`
	Type                 string    `json:"type"`
	IsPrimaryMeterRegion bool      `json:"isPrimaryMeterRegion"`
	ArmSkuName           string    `json:"armSkuName"`
	EffectiveEndDate     time.Time `json:"effectiveEndDate,omitempty"`
	ReservationTerm      string    `json:"reservationTerm,omitempty"`
}

type ProductsPricePage struct {
	BillingCurrency    string `json:"BillingCurrency"`
	CustomerEntityID   string `json:"CustomerEntityId"`
	CustomerEntityType string `json:"CustomerEntityType"`
	Items              []Item `json:"Items"`
	NextPageLink       string `json:"NextPageLink"`
	Count              int    `json:"Count"`
}
