package ngkex

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gotradebot/common"
	"gotradebot/config"
	"gotradebot/currency"
	exchange "gotradebot/exchanges"
	"gotradebot/exchanges/request"
	"gotradebot/exchanges/ticker"
	"gotradebot/exchanges/wshandler"
	log "gotradebot/logger"
)

const (
	ngkexAPIURL     = "https://api.ngkex.io"
	ngkexAPIVersion = "1"

	ngkexMarketHistoryKline    = "market/history/kline"
	ngkexMarketDetail          = "market/detail"
	ngkexMarketDetailMerged    = "market/detail/merged"
	ngkexMarketDepth           = "market/depth"
	ngkexMarketTrade           = "market/trade"
	ngkexMarketTradeHistory    = "market/history/trade"
	ngkexSymbols               = "common/symbols"
	ngkexCurrencies            = "common/currencys"
	ngkexTimestamp             = "common/timestamp"
	ngkexAccounts              = "account/accounts"
	ngkexAccountBalance        = "account/accounts/%s/balance"
	ngkexAggregatedBalance     = "subuser/aggregate-balance"
	ngkexOrderPlace            = "order/orders/place"
	ngkexOrderCancel           = "order/orders/%s/submitcancel"
	ngkexOrderCancelBatch      = "order/orders/batchcancel"
	ngkexBatchCancelOpenOrders = "order/orders/batchCancelOpenOrders"
	ngkexGetOrder              = "order/orders/%s"
	ngkexGetOrderMatch         = "order/orders/%s/matchresults"
	ngkexGetOrders             = "order/orders"
	ngkexGetOpenOrders         = "order/openOrders"
	ngkexGetOrdersMatch        = "orders/matchresults"
	ngkexMarginTransferIn      = "dw/transfer-in/margin"
	ngkexMarginTransferOut     = "dw/transfer-out/margin"
	ngkexMarginOrders          = "margin/orders"
	ngkexMarginRepay           = "margin/orders/%s/repay"
	ngkexMarginLoanOrders      = "margin/loan-orders"
	ngkexMarginAccountBalance  = "margin/accounts/balance"
	ngkexWithdrawCreate        = "dw/withdraw/api/create"
	ngkexWithdrawCancel        = "dw/withdraw-virtual/%s/cancel"

	ngkexAuthRate   = 100
	ngkexUnauthRate = 100
)

// NGKEX is the overarching type across this package
type NGKEX struct {
	exchange.Base
	AccountID                  string
	WebsocketConn              *wshandler.WebsocketConnection
	AuthenticatedWebsocketConn *wshandler.WebsocketConnection
}

// SetDefaults sets default values for the exchange
func (h *NGKEX) SetDefaults() {
	h.Name = "Ngkex"
	h.Enabled = false
	h.Fee = 0
	h.Verbose = false
	h.RESTPollingDelay = 10
	h.APIWithdrawPermissions = exchange.AutoWithdrawCryptoWithSetup |
		exchange.NoFiatWithdrawals
	h.RequestCurrencyPairFormat.Delimiter = ""
	h.RequestCurrencyPairFormat.Uppercase = false
	h.ConfigCurrencyPairFormat.Delimiter = "-"
	h.ConfigCurrencyPairFormat.Uppercase = true
	h.AssetTypes = []string{ticker.Spot}
	h.SupportsAutoPairUpdating = true
	h.SupportsRESTTickerBatching = false
	h.Requester = request.New(h.Name,
		request.NewRateLimit(time.Second*10, ngkexAuthRate),
		request.NewRateLimit(time.Second*10, ngkexUnauthRate),
		common.NewHTTPClientWithTimeout(exchange.DefaultHTTPTimeout))
	h.APIUrlDefault = ngkexAPIURL
	h.APIUrl = h.APIUrlDefault
	h.Websocket = wshandler.New()
	h.Websocket.Functionality = wshandler.WebsocketKlineSupported |
		wshandler.WebsocketOrderbookSupported |
		wshandler.WebsocketTradeDataSupported |
		wshandler.WebsocketSubscribeSupported |
		wshandler.WebsocketUnsubscribeSupported |
		wshandler.WebsocketAuthenticatedEndpointsSupported |
		wshandler.WebsocketAccountDataSupported |
		wshandler.WebsocketMessageCorrelationSupported
	h.WebsocketResponseMaxLimit = exchange.DefaultWebsocketResponseMaxLimit
	h.WebsocketResponseCheckTimeout = exchange.DefaultWebsocketResponseCheckTimeout
}

// Setup sets user configuration
func (h *NGKEX) Setup(exch *config.ExchangeConfig) {
	if !exch.Enabled {
		h.SetEnabled(false)
	} else {
		h.Enabled = true
		h.AuthenticatedAPISupport = exch.AuthenticatedAPISupport
		h.AuthenticatedWebsocketAPISupport = exch.AuthenticatedWebsocketAPISupport
		h.SetAPIKeys(exch.APIKey, exch.APISecret, "", false)
		h.APIAuthPEMKeySupport = exch.APIAuthPEMKeySupport
		h.APIAuthPEMKey = exch.APIAuthPEMKey
		h.SetHTTPClientTimeout(exch.HTTPTimeout)
		h.SetHTTPClientUserAgent(exch.HTTPUserAgent)
		h.RESTPollingDelay = exch.RESTPollingDelay
		h.Verbose = exch.Verbose
		h.HTTPDebugging = exch.HTTPDebugging
		h.Websocket.SetWsStatusAndConnection(exch.Websocket)
		h.BaseCurrencies = exch.BaseCurrencies
		h.AvailablePairs = exch.AvailablePairs
		h.EnabledPairs = exch.EnabledPairs
		err := h.SetCurrencyPairFormat()
		if err != nil {
			log.Fatal(err)
		}
		err = h.SetAssetTypes()
		if err != nil {
			log.Fatal(err)
		}
		err = h.SetAutoPairDefaults()
		if err != nil {
			log.Fatal(err)
		}
		err = h.SetAPIURL(exch)
		if err != nil {
			log.Fatal(err)
		}
		err = h.SetClientProxyAddress(exch.ProxyAddress)
		if err != nil {
			log.Fatal(err)
		}
		err = h.Websocket.Setup(h.WsConnect,
			h.Subscribe,
			h.Unsubscribe,
			exch.Name,
			exch.Websocket,
			exch.Verbose,
			wsMarketURL,
			exch.WebsocketURL,
			exch.AuthenticatedWebsocketAPISupport)
		if err != nil {
			log.Fatal(err)
		}
		h.WebsocketConn = &wshandler.WebsocketConnection{
			ExchangeName:         h.Name,
			URL:                  wsMarketURL,
			ProxyURL:             h.Websocket.GetProxyAddress(),
			Verbose:              h.Verbose,
			RateLimit:            rateLimit,
			ResponseCheckTimeout: exch.WebsocketResponseCheckTimeout,
			ResponseMaxLimit:     exch.WebsocketResponseMaxLimit,
		}
		h.AuthenticatedWebsocketConn = &wshandler.WebsocketConnection{
			ExchangeName:         h.Name,
			URL:                  wsAccountsOrdersURL,
			ProxyURL:             h.Websocket.GetProxyAddress(),
			Verbose:              h.Verbose,
			RateLimit:            rateLimit,
			ResponseCheckTimeout: exch.WebsocketResponseCheckTimeout,
			ResponseMaxLimit:     exch.WebsocketResponseMaxLimit,
		}
	}
}

// GetSpotKline returns kline data
// KlinesRequestParams contains symbol, period and size
func (h *NGKEX) GetSpotKline(arg KlinesRequestParams) ([]KlineItem, error) {
	vals := url.Values{}
	vals.Set("symbol", arg.Symbol)
	vals.Set("period", string(arg.Period))

	if arg.Size != 0 {
		vals.Set("size", strconv.Itoa(arg.Size))
	}

	type response struct {
		Response
		Data []KlineItem `json:"data"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketHistoryKline)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Data, err
}

// GetMarketDetailMerged returns the ticker for the specified symbol
func (h *NGKEX) GetMarketDetailMerged(symbol string) (DetailMerged, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)

	type response struct {
		Response
		Tick DetailMerged `json:"tick"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketDetailMerged)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return result.Tick, errors.New(result.ErrorMessage)
	}
	return result.Tick, err
}

// GetDepth returns the depth for the specified symbol
func (h *NGKEX) GetDepth(obd OrderBookDataRequestParams) (Orderbook, error) {
	vals := url.Values{}
	vals.Set("symbol", obd.Symbol)

	if obd.Type != OrderBookDataRequestParamsTypeNone {
		vals.Set("type", string(obd.Type))
	}

	type response struct {
		Response
		Depth Orderbook `json:"tick"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketDepth)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return result.Depth, errors.New(result.ErrorMessage)
	}
	return result.Depth, err
}

// GetTrades returns the trades for the specified symbol
func (h *NGKEX) GetTrades(symbol string) ([]Trade, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)

	type response struct {
		Response
		Tick struct {
			Data []Trade `json:"data"`
		} `json:"tick"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketTrade)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Tick.Data, err
}

// GetLatestSpotPrice returns latest spot price of symbol
//
// symbol: string of currency pair
func (h *NGKEX) GetLatestSpotPrice(symbol string) (float64, error) {
	list, err := h.GetTradeHistory(symbol, "1")

	if err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, errors.New("the length of the list is 0")
	}

	return list[0].Trades[0].Price, nil
}

// GetTradeHistory returns the trades for the specified symbol
func (h *NGKEX) GetTradeHistory(symbol, size string) ([]TradeHistory, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)

	if size != "" {
		vals.Set("size", size)
	}

	type response struct {
		Response
		TradeHistory []TradeHistory `json:"data"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketTradeHistory)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.TradeHistory, err
}

// GetMarketDetail returns the ticker for the specified symbol
func (h *NGKEX) GetMarketDetail(symbol string) (Detail, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)

	type response struct {
		Response
		Tick Detail `json:"tick"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/%s", h.APIUrl, ngkexMarketDetail)

	err := h.SendHTTPRequest(common.EncodeURLValues(urlPath, vals), &result)
	if result.ErrorMessage != "" {
		return result.Tick, errors.New(result.ErrorMessage)
	}
	return result.Tick, err
}

// GetSymbols returns an array of symbols supported by Huobi
func (h *NGKEX) GetSymbols() ([]Symbol, error) {
	type response struct {
		Response
		Symbols []Symbol `json:"data"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/v%s/%s", h.APIUrl, ngkexAPIVersion, ngkexSymbols)

	err := h.SendHTTPRequest(urlPath, &result)
	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Symbols, err
}

// GetCurrencies returns a list of currencies supported by Huobi
func (h *NGKEX) GetCurrencies() ([]string, error) {
	type response struct {
		Response
		Currencies []string `json:"data"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/v%s/%s", h.APIUrl, ngkexAPIVersion, ngkexCurrencies)

	err := h.SendHTTPRequest(urlPath, &result)
	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Currencies, err
}

// GetTimestamp returns the Huobi server time
func (h *NGKEX) GetTimestamp() (int64, error) {
	type response struct {
		Response
		Timestamp int64 `json:"data"`
	}

	var result response
	urlPath := fmt.Sprintf("%s/v%s/%s", h.APIUrl, ngkexAPIVersion, ngkexTimestamp)

	err := h.SendHTTPRequest(urlPath, &result)
	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.Timestamp, err
}

// GetAccounts returns the Huobi user accounts
func (h *NGKEX) GetAccounts() ([]Account, error) {
	type response struct {
		Response
		AccountData []Account `json:"data"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexAccounts, url.Values{}, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.AccountData, err
}

// GetAccountBalance returns the users Huobi account balance
func (h *NGKEX) GetAccountBalance(accountID string) ([]AccountBalanceDetail, error) {
	type response struct {
		Response
		AccountBalanceData AccountBalance `json:"data"`
	}

	var result response
	endpoint := fmt.Sprintf(ngkexAccountBalance, accountID)

	v := url.Values{}
	v.Set("account-id", accountID)

	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, endpoint, v, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.AccountBalanceData.AccountBalanceDetails, err
}

// GetAggregatedBalance returns the balances of all the sub-account aggregated.
func (h *NGKEX) GetAggregatedBalance() ([]AggregatedBalance, error) {
	type response struct {
		Response
		AggregatedBalances []AggregatedBalance `json:"data"`
	}

	var result response

	err := h.SendAuthenticatedHTTPRequest(
		http.MethodGet,
		ngkexAggregatedBalance,
		nil,
		nil,
		&result,
	)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.AggregatedBalances, err
}

// SpotNewOrder submits an order to Huobi
func (h *NGKEX) SpotNewOrder(arg SpotNewOrderRequestParams) (int64, error) {
	data := struct {
		AccountID int    `json:"account-id,string"`
		Amount    string `json:"amount"`
		Price     string `json:"price"`
		Source    string `json:"source"`
		Symbol    string `json:"symbol"`
		Type      string `json:"type"`
	}{
		AccountID: arg.AccountID,
		Amount:    strconv.FormatFloat(arg.Amount, 'f', -1, 64),
		Symbol:    arg.Symbol,
		Type:      string(arg.Type),
	}

	// Only set price if order type is not equal to buy-market or sell-market
	if arg.Type != SpotNewOrderRequestTypeBuyMarket && arg.Type != SpotNewOrderRequestTypeSellMarket {
		data.Price = strconv.FormatFloat(arg.Price, 'f', -1, 64)
	}

	if arg.Source != "" {
		data.Source = arg.Source
	}

	type response struct {
		Response
		OrderID int64 `json:"data,string"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, ngkexOrderPlace, nil, data, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.OrderID, err
}

// CancelExistingOrder cancels an order on Huobi
func (h *NGKEX) CancelExistingOrder(orderID int64) (int64, error) {
	type response struct {
		Response
		OrderID int64 `json:"data,string"`
	}

	var result response
	endpoint := fmt.Sprintf(ngkexOrderCancel, strconv.FormatInt(orderID, 10))
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, endpoint, url.Values{}, nil, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.OrderID, err
}

// CancelOrderBatch cancels a batch of orders -- to-do
func (h *NGKEX) CancelOrderBatch(_ []int64) ([]CancelOrderBatch, error) {
	type response struct {
		Response
		Data []CancelOrderBatch `json:"data"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, ngkexOrderCancelBatch, url.Values{}, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Data, err
}

// CancelOpenOrdersBatch cancels a batch of orders -- to-do
func (h *NGKEX) CancelOpenOrdersBatch(accountID, symbol string) (CancelOpenOrdersBatch, error) {
	params := url.Values{}

	params.Set("account-id", accountID)
	var result CancelOpenOrdersBatch

	data := struct {
		AccountID string `json:"account-id"`
		Symbol    string `json:"symbol"`
	}{
		AccountID: accountID,
		Symbol:    symbol,
	}

	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, ngkexBatchCancelOpenOrders, url.Values{}, data, &result)
	if result.Data.FailedCount > 0 {
		return result, fmt.Errorf("there were %v failed order cancellations", result.Data.FailedCount)
	}

	return result, err
}

// GetOrder returns order information for the specified order
func (h *NGKEX) GetOrder(orderID int64) (OrderInfo, error) {
	type response struct {
		Response
		Order OrderInfo `json:"data"`
	}

	var result response
	endpoint := fmt.Sprintf(ngkexGetOrder, strconv.FormatInt(orderID, 10))
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, endpoint, url.Values{}, nil, &result)

	if result.ErrorMessage != "" {
		return result.Order, errors.New(result.ErrorMessage)
	}
	return result.Order, err
}

// GetOrderMatchResults returns matched order info for the specified order
func (h *NGKEX) GetOrderMatchResults(orderID int64) ([]OrderMatchInfo, error) {
	type response struct {
		Response
		Orders []OrderMatchInfo `json:"data"`
	}

	var result response
	endpoint := fmt.Sprintf(ngkexGetOrderMatch, strconv.FormatInt(orderID, 10))
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, endpoint, url.Values{}, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Orders, err
}

// GetOrders returns a list of orders
func (h *NGKEX) GetOrders(symbol, types, start, end, states, from, direct, size string) ([]OrderInfo, error) {
	type response struct {
		Response
		Orders []OrderInfo `json:"data"`
	}

	vals := url.Values{}
	vals.Set("symbol", symbol)
	vals.Set("states", states)

	if types != "" {
		vals.Set("types", types)
	}

	if start != "" {
		vals.Set("start-date", start)
	}

	if end != "" {
		vals.Set("end-date", end)
	}

	if from != "" {
		vals.Set("from", from)
	}

	if direct != "" {
		vals.Set("direct", direct)
	}

	if size != "" {
		vals.Set("size", size)
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexGetOrders, vals, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Orders, err
}

// GetOpenOrders returns a list of orders
func (h *NGKEX) GetOpenOrders(accountID, symbol, side string, size int) ([]OrderInfo, error) {
	type response struct {
		Response
		Orders []OrderInfo `json:"data"`
	}

	vals := url.Values{}
	vals.Set("symbol", symbol)
	vals.Set("accountID", accountID)
	if len(side) > 0 {
		vals.Set("side", side)
	}
	vals.Set("size", fmt.Sprintf("%v", size))

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexGetOpenOrders, vals, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}

	return result.Orders, err
}

// GetOrdersMatch returns a list of matched orders
func (h *NGKEX) GetOrdersMatch(symbol, types, start, end, from, direct, size string) ([]OrderMatchInfo, error) {
	type response struct {
		Response
		Orders []OrderMatchInfo `json:"data"`
	}

	vals := url.Values{}
	vals.Set("symbol", symbol)

	if types != "" {
		vals.Set("types", types)
	}

	if start != "" {
		vals.Set("start-date", start)
	}

	if end != "" {
		vals.Set("end-date", end)
	}

	if from != "" {
		vals.Set("from", from)
	}

	if direct != "" {
		vals.Set("direct", direct)
	}

	if size != "" {
		vals.Set("size", size)
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexGetOrdersMatch, vals, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Orders, err
}

// MarginTransfer transfers assets into or out of the margin account
func (h *NGKEX) MarginTransfer(symbol, currency string, amount float64, in bool) (int64, error) {
	data := struct {
		Symbol   string `json:"symbol"`
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}{
		Symbol:   symbol,
		Currency: currency,
		Amount:   strconv.FormatFloat(amount, 'f', -1, 64),
	}

	path := ngkexMarginTransferIn
	if !in {
		path = ngkexMarginTransferOut
	}

	type response struct {
		Response
		TransferID int64 `json:"data"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, path, nil, data, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.TransferID, err
}

// MarginOrder submits a margin order application
func (h *NGKEX) MarginOrder(symbol, currency string, amount float64) (int64, error) {
	data := struct {
		Symbol   string `json:"symbol"`
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}{
		Symbol:   symbol,
		Currency: currency,
		Amount:   strconv.FormatFloat(amount, 'f', -1, 64),
	}

	type response struct {
		Response
		MarginOrderID int64 `json:"data"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, ngkexMarginOrders, nil, data, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.MarginOrderID, err
}

// MarginRepayment repays a margin amount for a margin ID
func (h *NGKEX) MarginRepayment(orderID int64, amount float64) (int64, error) {
	data := struct {
		Amount string `json:"amount"`
	}{
		Amount: strconv.FormatFloat(amount, 'f', -1, 64),
	}

	type response struct {
		Response
		MarginOrderID int64 `json:"data"`
	}

	var result response
	endpoint := fmt.Sprintf(ngkexMarginRepay, strconv.FormatInt(orderID, 10))
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, endpoint, nil, data, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.MarginOrderID, err
}

// GetMarginLoanOrders returns the margin loan orders
func (h *NGKEX) GetMarginLoanOrders(symbol, currency, start, end, states, from, direct, size string) ([]MarginOrder, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)
	vals.Set("currency", currency)

	if start != "" {
		vals.Set("start-date", start)
	}

	if end != "" {
		vals.Set("end-date", end)
	}

	if states != "" {
		vals.Set("states", states)
	}

	if from != "" {
		vals.Set("from", from)
	}

	if direct != "" {
		vals.Set("direct", direct)
	}

	if size != "" {
		vals.Set("size", size)
	}

	type response struct {
		Response
		MarginLoanOrders []MarginOrder `json:"data"`
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexMarginLoanOrders, vals, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.MarginLoanOrders, err
}

// GetMarginAccountBalance returns the margin account balances
func (h *NGKEX) GetMarginAccountBalance(symbol string) ([]MarginAccountBalance, error) {
	type response struct {
		Response
		Balances []MarginAccountBalance `json:"data"`
	}

	vals := url.Values{}
	if symbol != "" {
		vals.Set("symbol", symbol)
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodGet, ngkexMarginAccountBalance, vals, nil, &result)

	if result.ErrorMessage != "" {
		return nil, errors.New(result.ErrorMessage)
	}
	return result.Balances, err
}

// Withdraw withdraws the desired amount and currency
func (h *NGKEX) Withdraw(c currency.Code, address, addrTag string, amount, fee float64) (int64, error) {
	type response struct {
		Response
		WithdrawID int64 `json:"data"`
	}

	data := struct {
		Address  string `json:"address"`
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
		Fee      string `json:"fee,omitempty"`
		AddrTag  string `json:"addr-tag,omitempty"`
	}{
		Address:  address,
		Currency: c.Lower().String(),
		Amount:   strconv.FormatFloat(amount, 'f', -1, 64),
	}

	if fee > 0 {
		data.Fee = strconv.FormatFloat(fee, 'f', -1, 64)
	}

	if c == currency.XRP {
		data.AddrTag = addrTag
	}

	var result response
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, ngkexWithdrawCreate, nil, data, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.WithdrawID, err
}

// CancelWithdraw cancels a withdraw request
func (h *NGKEX) CancelWithdraw(withdrawID int64) (int64, error) {
	type response struct {
		Response
		WithdrawID int64 `json:"data"`
	}

	vals := url.Values{}
	vals.Set("withdraw-id", strconv.FormatInt(withdrawID, 10))

	var result response
	endpoint := fmt.Sprintf(ngkexWithdrawCancel, strconv.FormatInt(withdrawID, 10))
	err := h.SendAuthenticatedHTTPRequest(http.MethodPost, endpoint, vals, nil, &result)

	if result.ErrorMessage != "" {
		return 0, errors.New(result.ErrorMessage)
	}
	return result.WithdrawID, err
}

// SendHTTPRequest sends an unauthenticated HTTP request
func (h *NGKEX) SendHTTPRequest(path string, result interface{}) error {
	return h.SendPayload(http.MethodGet, path, nil, nil, result, false, false, h.Verbose, h.HTTPDebugging)
}

// SendAuthenticatedHTTPRequest sends authenticated requests to the NGKEX API
func (h *NGKEX) SendAuthenticatedHTTPRequest(method, endpoint string, values url.Values, data, result interface{}) error {
	if !h.AuthenticatedAPISupport {
		return fmt.Errorf(exchange.WarningAuthenticatedRequestWithoutCredentialsSet, h.Name)
	}

	if values == nil {
		values = url.Values{}
	}

	values.Set("AccessKeyId", h.APIKey)
	values.Set("SignatureMethod", "HmacSHA256")
	values.Set("SignatureVersion", "2")
	values.Set("Timestamp", time.Now().UTC().Format("2006-01-02T15:04:05"))

	endpoint = fmt.Sprintf("/v%s/%s", ngkexAPIVersion, endpoint)
	payload := fmt.Sprintf("%s\napi.ngkex.io\n%s\n%s",
		method, endpoint, values.Encode())

	headers := make(map[string]string)

	if method == http.MethodGet {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	} else {
		headers["Content-Type"] = "application/json"
	}

	hmac := common.GetHMAC(common.HashSHA256, []byte(payload), []byte(h.APISecret))
	signature := common.Base64Encode(hmac)
	values.Set("Signature", signature)

	if h.APIAuthPEMKeySupport {
		pemKey := strings.NewReader(h.APIAuthPEMKey)
		pemBytes, err := ioutil.ReadAll(pemKey)
		if err != nil {
			return fmt.Errorf("%s unable to ioutil.ReadAll PEM key: %s", h.Name, err)
		}

		block, _ := pem.Decode(pemBytes)
		if block == nil {
			return fmt.Errorf("%s PEM block is nil", h.Name)
		}

		x509Encoded := block.Bytes
		privKey, err := x509.ParseECPrivateKey(x509Encoded)
		if err != nil {
			return fmt.Errorf("%s unable to ParseECPrivKey: %s", h.Name, err)
		}

		r, s, err := ecdsa.Sign(rand.Reader, privKey, common.GetSHA256([]byte(signature)))
		if err != nil {
			return fmt.Errorf("%s unable to sign: %s", h.Name, err)
		}

		privSig := r.Bytes()
		privSig = append(privSig, s.Bytes()...)
		values.Set("PrivateSignature", common.Base64Encode(privSig))
	}

	urlPath := common.EncodeURLValues(
		fmt.Sprintf("%s%s", h.APIUrl, endpoint), values,
	)

	var body []byte

	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("%s unable to marshal data: %s", h.Name, err)
		}

		body = encoded
	}

	return h.SendPayload(method, urlPath, headers, bytes.NewReader(body), result, true, false, h.Verbose, h.HTTPDebugging)
}

// GetFee returns an estimate of fee based on type of transaction
func (h *NGKEX) GetFee(feeBuilder *exchange.FeeBuilder) (float64, error) {
	var fee float64
	if feeBuilder.FeeType == exchange.OfflineTradeFee || feeBuilder.FeeType == exchange.CryptocurrencyTradeFee {
		fee = calculateTradingFee(feeBuilder.Pair, feeBuilder.PurchasePrice, feeBuilder.Amount)
	}
	if fee < 0 {
		fee = 0
	}

	return fee, nil
}

func calculateTradingFee(c currency.Pair, price, amount float64) float64 {
	if c.IsCryptoFiatPair() {
		return 0.001 * price * amount
	}
	return 0.002 * price * amount
}
