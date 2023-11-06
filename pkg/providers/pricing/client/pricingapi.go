// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	apiVersion = "2021-10-01-preview"
	pricingURL = "https://prices.azure.com/api/retail/prices?api-version=" + apiVersion
)

type PricingAPI interface {
	GetProductsPricePages(context.Context, []*Filter, func(output *ProductsPricePage)) error
}

type pricingAPI struct{}

func New() PricingAPI {
	return &pricingAPI{}
}

func (papi *pricingAPI) GetProductsPricePages(_ context.Context, filters []*Filter, pageHandler func(output *ProductsPricePage)) error {
	nextURL := pricingURL

	if len(filters) > 0 {
		filterParams := []string{}
		for _, filter := range filters {
			filterParams = append(filterParams, filter.String())
		}

		filterParamsEscaped := url.QueryEscape(strings.Join(filterParams[:], " and "))

		nextURL += fmt.Sprintf("&$filter=%s", filterParamsEscaped)
	}

	for nextURL != "" {
		res, err := http.Get(nextURL)
		if err != nil {
			return err
		}

		if res.StatusCode != 200 {
			return fmt.Errorf("got a non-200 status code: %d", res.StatusCode)
		}

		resBody, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}

		page := ProductsPricePage{}
		err = json.Unmarshal(resBody, &page)
		if err != nil {
			return err
		}

		pageHandler(&page)
		nextURL = page.NextPageLink
	}
	return nil
}
