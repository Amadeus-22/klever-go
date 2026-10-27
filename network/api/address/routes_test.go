package address_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	mmock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/network/api/address"
	"github.com/klever-io/klever-go/network/api/middleware"
	"github.com/klever-io/klever-go/network/api/mock"
	"github.com/klever-io/klever-go/network/api/models"
	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/klever-io/klever-go/network/api/wrapper"
	"github.com/stretchr/testify/assert"
)

const validAddress = "klv17e8zzgn73h6ehe3c6q9vlt77kuxk5euddmhymy5uhv2rhv0dc0nqlfp0ap"

func init() {
	gin.SetMode(gin.TestMode)
}

func TestAddressRoute_EmptyTrailReturns404(t *testing.T) {
	t.Parallel()
	facade := mock.Facade{}
	ws := startNodeServer(&facade)

	req, _ := http.NewRequest("GET", "/address", nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestGetKDA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, asset string) (*kapps.UserKDA, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address and asset",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return &kapps.UserKDA{Balance: 100}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"userKDA": map[string]interface{}{"Balance": 100.0}, "address": validAddress, "asset": "KLV"},
		},
		{
			name:         "Success with default asset",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return &kapps.UserKDA{Balance: 100}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"userKDA": map[string]interface{}{"Balance": 100.0}, "address": validAddress, "asset": "KLV"},
		},
		{
			name:         "Missing address parameter",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil, // Not needed since the handler won't be called
			expectedCode: http.StatusBadRequest,
			expectedErr:  "get userKDA error: address is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set up mock facade
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.GetUserKDAHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)

			// Construct URL
			url := fmt.Sprintf("/address/%s/kda", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response map[string]interface{}
			if tc.expectedErr != "" {
				// Verify error response
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response["error"], tc.expectedErr)
			} else {
				// Verify success response
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				for key, value := range tc.expectedData {
					assert.Equal(t, value, response["data"].(map[string]interface{})[key])
				}
			}
		})
	}
}

func TestGetAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		mockHandler  func(address string) (state.UserAccountHandler, error)
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Success with valid address",
			addressParam: validAddress,
			mockHandler: func(address string) (state.UserAccountHandler, error) {
				return &mmock.AccountWrapMock{}, nil
			},
			expectedCode: http.StatusOK,
		},
		{
			name:         "Error from facade",
			addressParam: "invalidAddress",
			mockHandler: func(address string) (state.UserAccountHandler, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			facade.GetAccountHandler = tc.mockHandler

			ws := startNodeServer(&facade)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/address/%s", tc.addressParam), nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Empty(t, response.Error)
				assert.NotNil(t, response.Data)
			}
		})
	}
}

func TestGetBalance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, asset string) (*models.BalanceResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address and asset",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				return &models.BalanceResponse{Balance: 1000}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"balance": float64(1000)},
		},
		{
			name:         "Success with valid address and assetDefault",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				if asset == "KLV" {
					return &models.BalanceResponse{Balance: 1000}, nil
				}

				return &models.BalanceResponse{Balance: 0}, fmt.Errorf("asset not found")
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"balance": float64(1000)},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				return &models.BalanceResponse{Balance: 0}, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.BalanceHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/balance", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAccountNonce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		mockHandler  func(address string) (*models.AccountNonceResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address",
			addressParam: validAddress,
			mockHandler: func(address string) (*models.AccountNonceResponse, error) {
				return &models.AccountNonceResponse{
					Nonce:             1,
					FirstPendingNonce: 2,
					TxPending:         3,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"nonce":             float64(1),
				"firstPendingNonce": float64(2),
				"txPending":         float64(3),
			},
		},
		{
			name:         "Error from facade",
			addressParam: "invalidAddress",
			mockHandler: func(address string) (*models.AccountNonceResponse, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			facade.GetNextNonceHandler = tc.mockHandler

			ws := startNodeServer(&facade)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/address/%s/nonce", tc.addressParam), nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, assetId string) (*models.AvailableClaimResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid parameters",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{"KLV": 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"stakingRewards":    float64(100),
				"allStakingRewards": map[string]any{"KLV": float64(200)},
				"allowance":         float64(300),
			},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Empty asset",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset: assetId is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.RewardsAvailableToClaimHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/allowance", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaimList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetsQuery  string
		mockHandler  func(address, assetId string) (*models.AvailableClaimResponse, error)
		expectedCode int
		expectedData map[string]any
		expectedErr  string
	}{
		{
			name:         "Success with valid parameters",
			addressParam: validAddress,
			assetsQuery:  "KLV,BTC",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{assetId: 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"assets": map[string]interface{}{
					"KLV": map[string]interface{}{
						"stakingRewards":    float64(100),
						"allStakingRewards": map[string]interface{}{"KLV": float64(200)},
						"allowance":         float64(300),
					},
					"BTC": map[string]interface{}{
						"stakingRewards":    float64(100),
						"allStakingRewards": map[string]interface{}{"BTC": float64(200)},
						"allowance":         float64(300),
					},
				},
			},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetsQuery:  "KLV,BTC",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Empty assets",
			addressParam: validAddress,
			assetsQuery:  "",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("invalid assetID")
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset list: assetId is empty",
		},
		{
			name:         "Invalid assets",
			addressParam: validAddress,
			assetsQuery:  "KLV,INVALID",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				if assetId == "INVALID" {
					return nil, fmt.Errorf("invalid assetID")
				}

				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{assetId: 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset list: map[INVALID:invalid assetID]",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetsQuery:  "KLV,BTC",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("facade error for %s", assetId)
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.RewardsAvailableToClaimListHandler = perAssetClaimList(tc.mockHandler)
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/allowance/list", tc.addressParam)
			if tc.assetsQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetsQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaimListDeduplicatesRequestedAssets(t *testing.T) {
	t.Parallel()

	var lock sync.Mutex
	callsPerAsset := make(map[string]int)

	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: perAssetClaimList(func(address, assetId string) (*models.AvailableClaimResponse, error) {
			lock.Lock()
			callsPerAsset[assetId]++
			lock.Unlock()

			return &models.AvailableClaimResponse{
				StakingRewards:    100,
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		}),
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf("/address/%s/allowance/list?asset=%s", validAddress, "KLV,BTC,KLV,%20KLV%20,BTC,,KFI,KLV")

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response.Error)

	lock.Lock()
	defer lock.Unlock()
	assert.Equal(t, map[string]int{"KLV": 1, "BTC": 1, "KFI": 1}, callsPerAsset)

	assets := extractClaimListAssets(t, response)
	assert.Len(t, assets, 3)
	assert.Contains(t, assets, "KLV")
	assert.Contains(t, assets, "BTC")
	assert.Contains(t, assets, "KFI")
}

func TestGetAvailableClaimListRejectsExcessiveAssetCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		assetCount    int
		expectedCode  int
		expectedErr   string
		expectedCalls int64
	}{
		{
			name:          "at the maximum allowed asset count",
			assetCount:    address.MaxAssetsPerClaimList,
			expectedCode:  http.StatusOK,
			expectedCalls: int64(address.MaxAssetsPerClaimList),
		},
		{
			name:         "one asset above the maximum allowed asset count",
			assetCount:   address.MaxAssetsPerClaimList + 1,
			expectedCode: http.StatusBadRequest,
			expectedErr: fmt.Sprintf(
				"could not get rewards for requested account asset list: too many assets requested: maximum is %d",
				address.MaxAssetsPerClaimList,
			),
			expectedCalls: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64

			facade := mock.Facade{
				RewardsAvailableToClaimListHandler: perAssetClaimList(func(address, assetId string) (*models.AvailableClaimResponse, error) {
					calls.Add(1)

					return &models.AvailableClaimResponse{
						StakingRewards:    100,
						AllStakingRewards: map[string]int64{assetId: 200},
						Allowance:         300,
					}, nil
				}),
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf(
				"/address/%s/allowance/list?asset=%s",
				validAddress,
				strings.Join(buildRequestedAssets(tc.assetCount), ","),
			)

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			assert.Equal(t, tc.expectedCalls, calls.Load())

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Equal(t, tc.expectedErr, response.Error)
				assert.Equal(t, shared.ReturnCodeRequestError, response.Code)
				assert.Nil(t, response.Data)
				return
			}

			assert.Empty(t, response.Error)
			assert.Len(t, extractClaimListAssets(t, response), tc.assetCount)
		})
	}
}

func TestGetAvailableClaimListReturnsAllRequestedAssets(t *testing.T) {
	t.Parallel()

	requestedAssets := buildRequestedAssets(25)
	rewardsPerAsset := make(map[string]int64, len(requestedAssets))
	for i, asset := range requestedAssets {
		rewardsPerAsset[asset] = int64(100 + i)
	}

	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: perAssetClaimList(func(address, assetId string) (*models.AvailableClaimResponse, error) {
			return &models.AvailableClaimResponse{
				StakingRewards:    rewardsPerAsset[assetId],
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		}),
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response.Error)
	assert.Equal(t, shared.ReturnCodeSuccess, response.Code)

	assets := extractClaimListAssets(t, response)
	assert.Len(t, assets, len(requestedAssets))

	for _, asset := range requestedAssets {
		entry, ok := assets[asset].(map[string]interface{})
		if !assert.True(t, ok, asset) {
			continue
		}

		assert.Equal(t, float64(rewardsPerAsset[asset]), entry["stakingRewards"])
		assert.Equal(t, map[string]interface{}{asset: float64(200)}, entry["allStakingRewards"])
		assert.Equal(t, float64(300), entry["allowance"])
	}
}

func TestGetAvailableClaimListMakesSingleFacadeCall(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	var receivedAssets []string

	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: func(_ context.Context, addr string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
			calls.Add(1)
			receivedAssets = assetIds

			claims := make(map[string]*models.AvailableClaimResponse, len(assetIds))
			for _, assetId := range assetIds {
				claims[assetId] = &models.AvailableClaimResponse{StakingRewards: 100}
			}
			return claims, nil, nil
		},
	}

	requestedAssets := buildRequestedAssets(address.MaxAssetsPerClaimList)

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, requestedAssets, receivedAssets)
}

func TestGetAvailableClaimListReportsRequestErrorPerAsset(t *testing.T) {
	t.Parallel()

	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: func(context.Context, string, []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
			return nil, nil, fmt.Errorf("account not found")
		},
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf("/address/%s/allowance/list?asset=KLV,BTC", validAddress)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusBadRequest, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(
		t,
		"could not get rewards for requested account asset list: map[BTC:account not found KLV:account not found]",
		response.Error,
	)
	assert.Nil(t, response.Data)
}

// The lookup runs under a deadline and is not tied to the connection: net/http cancels the request
// context for a client that only closed its write side, and that client still gets its answer.
func TestGetAvailableClaimListRunsUnderDeadlineOnly(t *testing.T) {
	t.Parallel()

	reqCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()

	var calls atomic.Int64
	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: func(ctx context.Context, _ string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
			calls.Add(1)

			deadline, hasDeadline := ctx.Deadline()
			assert.True(t, hasDeadline, "the lookup must run under a request deadline")
			assert.LessOrEqual(t, time.Until(deadline), address.AvailableClaimListTimeout)

			cancelRequest()
			assert.NoError(t, ctx.Err(), "the request context's cancellation must not end the lookup")

			claims := make(map[string]*models.AvailableClaimResponse, len(assetIds))
			for _, assetId := range assetIds {
				claims[assetId] = &models.AvailableClaimResponse{StakingRewards: 100}
			}
			return claims, nil, nil
		},
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf("/address/%s/allowance/list?asset=KLV,BTC", validAddress)

	req, _ := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, extractClaimListAssets(t, response), 2)
}

func TestGetAvailableClaimListReportsTimeout(t *testing.T) {
	t.Parallel()

	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: func(context.Context, string, []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
			return nil, nil, context.DeadlineExceeded
		},
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf("/address/%s/allowance/list?asset=KLV,BTC", validAddress)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusServiceUnavailable, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, shared.ReturnCodeSystemBusy, response.Code)
	assert.Equal(t, "could not get rewards for requested account asset list: request timed out", response.Error)
	assert.Nil(t, response.Data)
}

// A lookup that returns a result after the deadline passed is a timeout too, not a late 200.
// It runs in a synctest bubble, so the real 10s deadline expires on a fake clock.
func TestGetAvailableClaimListReportsTimeoutWhenLookupFinishesLate(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		facade := mock.Facade{
			RewardsAvailableToClaimListHandler: func(ctx context.Context, _ string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
				// the last asset runs past the deadline and the lookup still succeeds
				<-ctx.Done()

				claims := make(map[string]*models.AvailableClaimResponse, len(assetIds))
				for _, assetId := range assetIds {
					claims[assetId] = &models.AvailableClaimResponse{StakingRewards: 100}
				}
				return claims, nil, nil
			},
		}

		ws := startNodeServer(&facade)
		url := fmt.Sprintf("/address/%s/allowance/list?asset=KLV,BTC", validAddress)

		start := time.Now()
		req, _ := http.NewRequest("GET", url, nil)
		resp := httptest.NewRecorder()
		ws.ServeHTTP(resp, req)

		// The literal is deliberate: it pins the intended deadline, so shortening the production
		// constant fails here instead of moving both sides of the comparison.
		assert.Equal(t, 10*time.Second, time.Since(start), "the lookup must end exactly at the intended deadline")
		assert.Equal(t, http.StatusServiceUnavailable, resp.Code)

		var response shared.GenericAPIResponse
		err := json.Unmarshal(resp.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, shared.ReturnCodeSystemBusy, response.Code)
		assert.Equal(t, "could not get rewards for requested account asset list: request timed out", response.Error)
		assert.Nil(t, response.Data)
	})
}

// A client that half-closes its connection after sending the request is still waiting for the
// answer. net/http cancels the request context when it reads EOF on such a connection, so this
// runs through a real server: the lookup waits until that cancellation has happened, and must
// not see it, and the client must still get its 200.
func TestGetAvailableClaimListAnswersHalfClosedClient(t *testing.T) {
	t.Parallel()

	requestCtx := make(chan context.Context, 1)
	facade := mock.Facade{
		RewardsAvailableToClaimListHandler: func(ctx context.Context, _ string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
			// wait for net/http to cancel the request context on the client's EOF
			select {
			case <-(<-requestCtx).Done():
			case <-time.After(3 * time.Second):
				t.Error("net/http never cancelled the request context of the half-closed client")
				return nil, nil, fmt.Errorf("request context was not cancelled")
			}

			if !assert.NoError(t, ctx.Err(), "the request context's cancellation must not reach the lookup") {
				return nil, nil, ctx.Err()
			}

			claims := make(map[string]*models.AvailableClaimResponse, len(assetIds))
			for _, assetId := range assetIds {
				claims[assetId] = &models.AvailableClaimResponse{StakingRewards: 100}
			}
			return claims, nil, nil
		},
	}

	ws := startNodeServer(&facade)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCtx <- r.Context()
		ws.ServeHTTP(w, r)
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if !assert.NoError(t, err) {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	_, err = fmt.Fprintf(conn, "GET /address/%s/allowance/list?asset=KLV,BTC HTTP/1.1\r\nHost: node\r\nConnection: close\r\n\r\n", validAddress)
	assert.NoError(t, err)
	assert.NoError(t, conn.(*net.TCPConn).CloseWrite())

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if !assert.NoError(t, err) {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response shared.GenericAPIResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&response))
	assert.Len(t, extractClaimListAssets(t, response), 2)
}

// perAssetClaimList adapts a per-asset claim mock into the list facade call, collecting
// per-asset errors the way the node does.
func perAssetClaimList(
	handler func(address, assetId string) (*models.AvailableClaimResponse, error),
) func(_ context.Context, address string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
	return func(_ context.Context, address string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error) {
		claims := make(map[string]*models.AvailableClaimResponse, len(assetIds))
		claimErrors := make(map[string]error)
		for _, assetId := range assetIds {
			claim, err := handler(address, assetId)
			if err != nil {
				claimErrors[assetId] = err
				continue
			}
			claims[assetId] = claim
		}

		return claims, claimErrors, nil
	}
}

func buildRequestedAssets(count int) []string {
	assets := make([]string, 0, count)
	for i := 0; i < count; i++ {
		assets = append(assets, fmt.Sprintf("KDA%d-A1B2", i))
	}

	return assets
}

func extractClaimListAssets(t *testing.T, response shared.GenericAPIResponse) map[string]interface{} {
	t.Helper()

	data, ok := response.Data.(map[string]interface{})
	if !assert.True(t, ok) {
		return nil
	}

	assets, ok := data["assets"].(map[string]interface{})
	if !assert.True(t, ok) {
		return nil
	}

	return assets
}

func TestInvalidFacade(t *testing.T) {
	t.Parallel()

	endpoints := []struct {
		name string
		path string
	}{
		{"GetAccount", "/address/test"},
		{"GetBalance", "/address/test/balance"},
		{"GetKDA", "/address/test/kda"},
		{"GetAccountNonce", "/address/test/nonce"},
		{"GetAvailableClaim", "/address/test/allowance?asset=KLV"},
		{"GetAvailableClaimList", "/address/test/allowance/list?asset=KLV"},
	}

	t.Run("nil facade", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				ws := startNodeServer(nil)
				req, _ := http.NewRequest("GET", endpoint.path, nil)
				resp := httptest.NewRecorder()
				ws.ServeHTTP(resp, req)

				assert.Equal(t, http.StatusInternalServerError, resp.Code)

				var response shared.GenericAPIResponse
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response.Error, "nil app context")
			})
		}
	})

	t.Run("invalid facade type", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				ws := gin.New()
				invalidFacade := "invalid facade"
				addressGroup := ws.Group("/address")
				addressGroup.Use(func(c *gin.Context) {
					c.Set("facade", invalidFacade) // Set invalid facade type
					c.Next()
				})
				addressRoute, _ := wrapper.NewRouterWrapper("address", addressGroup, getRoutesConfig())
				address.Routes(addressRoute)

				req, _ := http.NewRequest("GET", endpoint.path, nil)
				resp := httptest.NewRecorder()
				ws.ServeHTTP(resp, req)

				assert.Equal(t, http.StatusInternalServerError, resp.Code)

				var response shared.GenericAPIResponse
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response.Error, "invalid app context")
			})
		}
	})
}

func startNodeServer(handler address.FacadeHandler) *gin.Engine {
	ws := gin.New()
	ws.Use(cors.Default())
	addressRoutes := ws.Group("/address")
	if handler != nil {
		addressRoutes.Use(middleware.WithFacade(handler))
	}
	addressRoute, _ := wrapper.NewRouterWrapper("address", addressRoutes, getRoutesConfig())
	address.Routes(addressRoute)
	return ws
}

func getRoutesConfig() config.APIRoutesConfig {
	return config.APIRoutesConfig{
		APIPackages: map[string]config.APIPackageConfig{
			"address": {
				Routes: []config.RouteConfig{
					{Name: "/:address/kda", Open: true},
					{Name: "/:address/balance", Open: true},
					{Name: "/:address/nonce", Open: true},
					{Name: "/:address/allowance", Open: true},
					{Name: "/:address/allowance/list", Open: true},
					{Name: "/:address", Open: true},
				},
			},
		},
	}
}
