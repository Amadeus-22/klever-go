package address

import (
	"context"
	stdErrors "errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/data/state"

	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/network/api/errors"
	"github.com/klever-io/klever-go/network/api/models"
	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/klever-io/klever-go/network/api/wrapper"
)

var log = logger.GetOrCreate("api/address")

const (
	getAccountPath            = "/:address"
	getBalancePath            = "/:address/balance"
	getKDAPath                = "/:address/kda"
	getAccountNoncePath       = "/:address/nonce"
	getAvailableClaimPath     = "/:address/allowance"
	getAvailableClaimListPath = "/:address/allowance/list"

	defaultAsset = "KLV"

	// maxAssetsPerClaimList bounds the per-request asset count. The user account and the
	// Staking and KDA kapp accounts are loaded once per request (KLC-2652), so a request costs
	// three exclusive AccountsDB acquisitions regardless of the asset count, plus one on the kapps
	// adapter for the Validators kapp (KLV pending rewards) when KLV is requested. Each asset still
	// costs three data-trie reads, computed sequentially, and each read takes that trie's
	// exclusive lock; the Staking and KDA data tries are shared with block processing through
	// the AccountsDB data-trie cache, so this cap is what bounds that remaining contention.
	maxAssetsPerClaimList = 25

	// availableClaimListTimeout is the deadline after which a claim-list request stops taking
	// new state locks and is answered as timed out. The node checks it before each state load,
	// before each asset and before the KLV pending-rewards load: a lookup already blocked on a
	// state lock is not interrupted, and keeps the request's global throttler slot until it returns.
	availableClaimListTimeout = 10 * time.Second
)

// FacadeHandler interface defines methods that can be used by the gin webserver
type FacadeHandler interface {
	GetAccount(address string) (state.UserAccountHandler, error)
	GetNextNonce(address string) (*models.AccountNonceResponse, error)
	GetBalance(address string, kda string) (*models.BalanceResponse, error)
	GetUserKDA(address string, kda string) (*kapps.UserKDA, error)
	GetAvailableClaim(address string, assetId string) (*models.AvailableClaimResponse, error)
	GetAvailableClaimList(ctx context.Context, address string, assetIds []string) (map[string]*models.AvailableClaimResponse, map[string]error, error)
	IsInterfaceNil() bool
}

// Routes defines address related routes
func Routes(router *wrapper.RouterWrapper) {
	router.RegisterHandler(http.MethodGet, getAccountPath, GetAccount)
	router.RegisterHandler(http.MethodGet, getBalancePath, GetBalance)
	router.RegisterHandler(http.MethodGet, getKDAPath, GetKDA)
	router.RegisterHandler(http.MethodGet, getAccountNoncePath, GetAccountNonce)
	router.RegisterHandler(http.MethodGet, getAvailableClaimPath, GetAvailableClaim)
	router.RegisterHandler(http.MethodGet, getAvailableClaimListPath, GetAvailableClaimList)
}

func getFacade(c *gin.Context) (FacadeHandler, bool) {
	facadeObj, ok := c.Get("facade")
	if !ok {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrNilAppContext),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return nil, false
	}

	facade, ok := facadeObj.(FacadeHandler)
	if !ok {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrInvalidAppContext),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return nil, false
	}

	return facade, true
}

// @Summary returns an accountResponse
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Success 200 object shared.GenericAPIResponse{data=object{account=data.AccountInfo}} "ok"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address} [get]
// GetAccount returns an accountResponse containing information
// about the account correlated with provided address
func GetAccount(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	acc, err := facade.GetAccount(addr)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrCouldNotGetAccount, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  gin.H{"account": acc},
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the nonce and pending transaction info for a specific account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Success 200 object shared.GenericAPIResponse{data=models.AccountNonceResponse} "ok"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/nonce [get]
// GetAccountNonce returns an account nonce info
func GetAccountNonce(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	nonceResponse, err := facade.GetNextNonce(addr)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrCouldNotGetAccount, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  nonceResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the balance for the address parameter
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string false "asset" default(KLV)
// @Success 200 object shared.GenericAPIResponse{data=models.BalanceResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/balance [get]
// GetBalance returns the balance for the address parameter
func GetBalance(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	asset := c.Query("asset")
	if asset == "" {
		asset = defaultAsset
	}

	addr := c.Param("address")
	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  &models.BalanceResponse{Balance: 0},
				Error: errors.APIErrorString(errors.ErrGetBalance, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	balanceResponse, err := facade.GetBalance(addr, asset)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  &models.BalanceResponse{Balance: 0},
				Error: errors.APIErrorString(errors.ErrGetBalance, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}
	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  balanceResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the user kda for the address parameter
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string false "asset" default(KLV)
// @Success 200 object shared.GenericAPIResponse{data=models.KDAResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/kda [get]
// GetKDA returns the balance for the address parameter
func GetKDA(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	asset := c.Query("asset")
	if asset == "" {
		asset = defaultAsset
	}

	addr := c.Param("address")
	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  &models.KDAResponse{UserKDA: nil, Address: addr, Asset: asset},
				Error: errors.APIErrorString(errors.ErrGetUserKDA, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	userKDA, err := facade.GetUserKDA(addr, asset)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  &models.KDAResponse{UserKDA: nil, Address: addr, Asset: asset},
				Error: errors.APIErrorString(errors.ErrGetUserKDA, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	response := &models.KDAResponse{
		UserKDA: userKDA,
		Address: addr,
		Asset:   asset,
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  response,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the rewards available for a specific asset in an account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string true "asset"
// @Success 200 object shared.GenericAPIResponse{data=models.AvailableClaimResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/allowance [get]
// GetAvailableClaim returns the rewards available for a specific asset in an account
func GetAvailableClaim(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	assetId := c.Query("asset")

	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}
	if assetId == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, errors.ErrEmptyAssetId),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	claimResponse, err := facade.GetAvailableClaim(addr, assetId)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  claimResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// parseRequestedAssets splits a comma-separated asset query into distinct, non-empty asset IDs.
//
// It deliberately returns up to maxAssetsPerClaimList+1 entries rather than truncating at the cap,
// so the caller can distinguish an at-cap request from an over-cap one and reject the latter with
// HTTP 400. Breaking one element earlier would silently serve partial results with HTTP 200.
func parseRequestedAssets(assets string) []string {
	raw := strings.Split(assets, ",")
	assetList := make([]string, 0, min(len(raw), maxAssetsPerClaimList+1))
	requested := make(map[string]struct{}, cap(assetList))

	for _, s := range raw {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		if _, alreadyRequested := requested[t]; alreadyRequested {
			continue
		}
		requested[t] = struct{}{}
		assetList = append(assetList, t)
		if len(assetList) > maxAssetsPerClaimList {
			break
		}
	}

	return assetList
}

// @Summary returns the rewards available for a specific list of asset in an account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string true "assets (comma-separated asset IDs, max 25), e.g. KLV,KFI"
// @Success 200 object shared.GenericAPIResponse{data=models.AvailableClaimListResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Failure 503 object shared.GenericAPIResponse "request timed out"
// @Router /address/{address}/allowance/list [get]
// GetAvailableClaimList returns the rewards available for a specific list of asset in an account
func GetAvailableClaimList(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	assets := c.Query("asset") // need to be separated by comma

	// split assets
	assetList := parseRequestedAssets(assets)

	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	if len(assetList) == 0 {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, errors.ErrEmptyAssetId),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	if len(assetList) > maxAssetsPerClaimList {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data: nil,
				Error: errors.APIErrorString(
					errors.ErrGetAvailableClaimList,
					fmt.Errorf("%w: maximum is %d", errors.ErrTooManyAssets, maxAssetsPerClaimList),
				),
				Code: shared.ReturnCodeRequestError,
			},
		)
		return
	}

	// One facade call per request: the account and kapp state is loaded once and shared by every
	// requested asset, so a request costs a constant number of accounts-adapter lock acquisitions.
	//
	// The lookup runs under availableClaimListTimeout, so a request that outlives it stops
	// instead of running every asset. The deadline is the only thing that ends it early: the
	// request context's cancellation is deliberately dropped. net/http cancels that context when
	// the read side of the connection reaches EOF, which also happens for a client that only
	// closed its write side and is still waiting for the answer it has always received. It is not
	// a bound either, since a client keeps the context alive by declaring a request body, which
	// this route ignores, or by sending one more byte after the headers.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), availableClaimListTimeout)
	defer cancel()

	assetData, allowanceError, err := facade.GetAvailableClaimList(ctx, addr, assetList)
	// The node checks the deadline before each asset but not after the last one, so a lookup can
	// return a result after the deadline passed. That is a timeout too: the context is checked
	// here so every request that outlives the deadline gets the same answer.
	if stdErrors.Is(err, context.DeadlineExceeded) || stdErrors.Is(ctx.Err(), context.DeadlineExceeded) {
		shared.RespondWith(
			c,
			http.StatusServiceUnavailable,
			nil,
			errors.APIErrorString(errors.ErrGetAvailableClaimList, errors.ErrRequestTimedOut),
			shared.ReturnCodeSystemBusy,
		)
		return
	}
	if err != nil {
		// keep the per-asset error shape clients already receive for request-level failures
		allowanceError = make(map[string]error, len(assetList))
		for _, assetId := range assetList {
			allowanceError[assetId] = err
		}
	}

	if len(allowanceError) > 0 {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, allowanceError),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	response := &models.AvailableClaimListResponse{
		Assets: assetData,
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  response,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}
