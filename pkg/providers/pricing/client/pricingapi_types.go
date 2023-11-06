// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

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
