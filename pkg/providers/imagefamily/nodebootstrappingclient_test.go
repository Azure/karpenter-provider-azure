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

package imagefamily

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTokenCredential implements the azcore.TokenCredential interface for testing
type mockTokenCredential struct {
	token azcore.AccessToken
	err   error
	calls int
}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	m.calls++
	return m.token, m.err
}

func testRefreshes(t *testing.T, testFixedTime time.Time, expiresOnOffset time.Duration, refreshOnOffset time.Duration, expectedRefreshTime time.Time, ignoreRefreshOn bool) {
	ctx := context.Background()
	cache := &tokenCache{}
	var refreshOn time.Time

	if ignoreRefreshOn {
		refreshOn = time.Time{} // No RefreshOn set
	} else {
		refreshOn = testFixedTime.Add(refreshOnOffset)
	}
	mockCred := &mockTokenCredential{token: azcore.AccessToken{
		Token:     "test-token",
		ExpiresOn: testFixedTime.Add(expiresOnOffset),
		RefreshOn: refreshOn,
	}}

	token, err := cache.getTokenAtTime(ctx, mockCred, testFixedTime)

	require.NoError(t, err)
	assert.Equal(t, mockCred.token, token)
	assert.GreaterOrEqual(t, 1, mockCred.calls, "Expected credential.GetToken to be called")

	// Time has passed, but still before expected refresh time
	testTime2 := expectedRefreshTime.Add(-1 * time.Second)
	if ignoreRefreshOn {
		refreshOn = time.Time{} // No RefreshOn set
	} else {
		refreshOn = testTime2.Add(refreshOnOffset)
	}
	mockCred2 := &mockTokenCredential{token: azcore.AccessToken{
		Token:     "test-token-2",
		ExpiresOn: testTime2.Add(expiresOnOffset),
		RefreshOn: refreshOn,
	}}
	token, err = cache.getTokenAtTime(ctx, mockCred2, testTime2)

	// Should return the same token
	require.NoError(t, err)
	assert.Equal(t, mockCred.token, token)
	assert.Equal(t, 0, mockCred2.calls, "Expected credential.GetToken not to be called")

	// Time has passed, now at expected refresh time
	testTime3 := expectedRefreshTime
	if ignoreRefreshOn {
		refreshOn = time.Time{} // No RefreshOn set
	} else {
		refreshOn = testTime3.Add(refreshOnOffset)
	}
	mockCred3 := &mockTokenCredential{token: azcore.AccessToken{
		Token:     "test-token-3",
		ExpiresOn: testTime3.Add(expiresOnOffset),
		RefreshOn: refreshOn,
	}}
	token, err = cache.getTokenAtTime(ctx, mockCred3, testTime3)

	// Should return the new token
	require.NoError(t, err)
	assert.Equal(t, mockCred3.token, token)
	assert.GreaterOrEqual(t, 1, mockCred3.calls, "Expected credential.GetToken to be called")
}

func TestTokenCacheGetTokenAtTime(t *testing.T) {
	testFixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("returns error when credential.GetToken fails", func(t *testing.T) {
		ctx := context.Background()
		cache := &tokenCache{}

		mockCred := &mockTokenCredential{err: errors.New("token error")}

		_, err := cache.getTokenAtTime(ctx, mockCred, testFixedTime)

		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to get token")
		assert.GreaterOrEqual(t, 1, mockCred.calls, "Expected credential.GetToken to be called")
	})

	t.Run("gets new token when cache is empty", func(t *testing.T) {
		ctx := context.Background()
		cache := &tokenCache{}

		mockCred := &mockTokenCredential{token: azcore.AccessToken{
			Token:     "test-token",
			ExpiresOn: testFixedTime.Add(2 * time.Minute),
		}}

		token, err := cache.getTokenAtTime(ctx, mockCred, testFixedTime)

		require.NoError(t, err)
		assert.Equal(t, mockCred.token, token)
		assert.GreaterOrEqual(t, 1, mockCred.calls, "Expected credential.GetToken to be called")
	})

	t.Run("refreshable at RefreshOn when it's before 1 minute", func(t *testing.T) {
		testRefreshes(t, testFixedTime, 2*time.Minute, 30*time.Second, testFixedTime.Add(30*time.Second), false)
	})

	t.Run("refreshable at ExpiresOn when it's before 1 minute", func(t *testing.T) {
		testRefreshes(t, testFixedTime, 30*time.Second, 0, testFixedTime.Add(30*time.Second), true)
	})

	t.Run("refreshable at RefreshOn when it's before 1 minute and ExpiresOn is also eligible", func(t *testing.T) {
		testRefreshes(t, testFixedTime, 40*time.Minute, 20*time.Second, testFixedTime.Add(20*time.Second), false)
	})

	t.Run("refreshable at 1 minute when RefreshOn is not provided and ExpiresOn is not eligible", func(t *testing.T) {
		testRefreshes(t, testFixedTime, 2*time.Minute, 0, testFixedTime.Add(1*time.Minute), true)
	})

	t.Run("refreshable at 1 minute when bot RefreshOn and ExpiresOn are not eligible", func(t *testing.T) {
		testRefreshes(t, testFixedTime, 4*time.Minute, 2*time.Minute, testFixedTime.Add(1*time.Minute), false)
	})
}
