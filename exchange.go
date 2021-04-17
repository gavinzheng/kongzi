package main

import (
	"errors"
	"gotradebot/exchanges/huobihadax"
	"strings"
	"sync"

	"gotradebot/common"
	exchange "gotradebot/exchanges"
	"gotradebot/exchanges/anx"
	"gotradebot/exchanges/binance"
	"gotradebot/exchanges/bitfinex"
	"gotradebot/exchanges/bitflyer"
	"gotradebot/exchanges/bithumb"
	"gotradebot/exchanges/bitmex"
	"gotradebot/exchanges/bitstamp"
	"gotradebot/exchanges/bittrex"
	"gotradebot/exchanges/btcmarkets"
	"gotradebot/exchanges/btse"
	"gotradebot/exchanges/coinbasepro"
	"gotradebot/exchanges/coinut"
	"gotradebot/exchanges/exmo"
	"gotradebot/exchanges/gateio"
	"gotradebot/exchanges/gemini"
	"gotradebot/exchanges/hitbtc"
	"gotradebot/exchanges/huobi"
	"gotradebot/exchanges/itbit"
	"gotradebot/exchanges/kraken"
	"gotradebot/exchanges/lakebtc"
	"gotradebot/exchanges/localbitcoins"
	"gotradebot/exchanges/ngkex"
	"gotradebot/exchanges/okcoin"
	"gotradebot/exchanges/okex"
	"gotradebot/exchanges/poloniex"
	"gotradebot/exchanges/yobit"
	"gotradebot/exchanges/zb"
	log "gotradebot/logger"
)

// vars related to exchange functions
var (
	ErrNoExchangesLoaded     = errors.New("no exchanges have been loaded")
	ErrExchangeNotFound      = errors.New("exchange not found")
	ErrExchangeAlreadyLoaded = errors.New("exchange already loaded")
	ErrExchangeFailedToLoad  = errors.New("exchange failed to load")
)

// CheckExchangeExists returns true whether or not an exchange has already
// been loaded
func CheckExchangeExists(exchName string) bool {
	for x := range bot.exchanges {
		if strings.EqualFold(bot.exchanges[x].GetName(), exchName) {
			return true
		}
	}
	return false
}

// GetExchangeByName returns an exchange given an exchange name
func GetExchangeByName(exchName string) exchange.IBotExchange {
	for x := range bot.exchanges {
		if strings.EqualFold(bot.exchanges[x].GetName(), exchName) {
			return bot.exchanges[x]
		}
	}
	return nil
}

// ReloadExchange loads an exchange config by name
func ReloadExchange(name string) error {
	if len(bot.exchanges) == 0 {
		return ErrNoExchangesLoaded
	}

	if !CheckExchangeExists(name) {
		return ErrExchangeNotFound
	}

	exchCfg, err := bot.config.GetExchangeConfig(name)
	if err != nil {
		return err
	}

	e := GetExchangeByName(name)
	e.Setup(&exchCfg)
	log.Debugf("%s exchange reloaded successfully.\n", name)
	return nil
}

// UnloadExchange unloads an exchange by name
func UnloadExchange(name string) error {
	if len(bot.exchanges) == 0 {
		return ErrNoExchangesLoaded
	}

	if !CheckExchangeExists(name) {
		return ErrExchangeNotFound
	}

	exchCfg, err := bot.config.GetExchangeConfig(name)
	if err != nil {
		return err
	}

	exchCfg.Enabled = false
	err = bot.config.UpdateExchangeConfig(&exchCfg)
	if err != nil {
		return err
	}

	for x := range bot.exchanges {
		if strings.EqualFold(bot.exchanges[x].GetName(), name) {
			bot.exchanges[x].SetEnabled(false)
			bot.exchanges = append(bot.exchanges[:x], bot.exchanges[x+1:]...)
			return nil
		}
	}

	return ErrExchangeNotFound
}

// LoadExchange loads an exchange by name
func LoadExchange(name string, useWG bool, wg *sync.WaitGroup) error {
	nameLower := common.StringToLower(name)
	var exch exchange.IBotExchange

	if len(bot.exchanges) > 0 {
		if CheckExchangeExists(name) {
			return ErrExchangeAlreadyLoaded
		}
	}

	switch nameLower {
	case "anx":
		exch = new(anx.ANX)
	case "binance":
		exch = new(binance.Binance)
	case "bitfinex":
		exch = new(bitfinex.Bitfinex)
	case "bitflyer":
		exch = new(bitflyer.Bitflyer)
	case "bithumb":
		exch = new(bithumb.Bithumb)
	case "bitmex":
		exch = new(bitmex.Bitmex)
	case "bitstamp":
		exch = new(bitstamp.Bitstamp)
	case "bittrex":
		exch = new(bittrex.Bittrex)
	case "btc markets":
		exch = new(btcmarkets.BTCMarkets)
	case "btse":
		exch = new(btse.BTSE)
	case "coinut":
		exch = new(coinut.COINUT)
	case "exmo":
		exch = new(exmo.EXMO)
	case "coinbasepro":
		exch = new(coinbasepro.CoinbasePro)
	case "gateio":
		exch = new(gateio.Gateio)
	case "gemini":
		exch = new(gemini.Gemini)
	case "hitbtc":
		exch = new(hitbtc.HitBTC)
	case "huobi":
		exch = new(huobi.HUOBI)
	case "huobihadax":
		exch = new(huobihadax.HUOBIHADAX)
	case "itbit":
		exch = new(itbit.ItBit)
	case "kraken":
		exch = new(kraken.Kraken)
	case "lakebtc":
		exch = new(lakebtc.LakeBTC)
	case "localbitcoins":
		exch = new(localbitcoins.LocalBitcoins)
	case "ngkex":
		exch = new(ngkex.NGKEX)
	case "okcoin international":
		exch = new(okcoin.OKCoin)
	case "okex":
		exch = new(okex.OKEX)
	case "poloniex":
		exch = new(poloniex.Poloniex)
	case "yobit":
		exch = new(yobit.Yobit)
	case "zb":
		exch = new(zb.ZB)
	default:
		return ErrExchangeNotFound
	}

	if exch == nil {
		return ErrExchangeFailedToLoad
	}

	exch.SetDefaults()
	bot.exchanges = append(bot.exchanges, exch)
	exchCfg, err := bot.config.GetExchangeConfig(name)
	if err != nil {
		return err
	}

	exchCfg.Enabled = true
	exch.Setup(&exchCfg)

	if useWG {
		exch.Start(wg)
	} else {
		wg := sync.WaitGroup{}
		exch.Start(&wg)
		wg.Wait()
	}
	return nil
}

// SetupExchanges sets up the exchanges used by the bot
func SetupExchanges() {
	var wg sync.WaitGroup
	for x := range bot.config.Exchanges {
		exch := &bot.config.Exchanges[x]
		if CheckExchangeExists(exch.Name) {
			e := GetExchangeByName(exch.Name)
			if e == nil {
				log.Errorf("%s", ErrExchangeNotFound)
				continue
			}

			err := ReloadExchange(exch.Name)
			if err != nil {
				log.Errorf("ReloadExchange %s failed: %s", exch.Name, err)
				continue
			}

			if !e.IsEnabled() {
				UnloadExchange(exch.Name)
				continue
			}
			return

		}
		if !exch.Enabled {
			log.Debugf("%s: Exchange support: Disabled", exch.Name)
			continue
		}
		err := LoadExchange(exch.Name, true, &wg)
		if err != nil {
			log.Errorf("LoadExchange %s failed: %s", exch.Name, err)
			continue
		}
		log.Debugf(
			"%s: Exchange support: Enabled (Authenticated API support: %s - Verbose mode: %s).\n",
			exch.Name,
			common.IsEnabled(exch.AuthenticatedAPISupport),
			common.IsEnabled(exch.Verbose),
		)
	}
	wg.Wait()
	if len(bot.exchanges) == 0 {
		log.Fatalf("No exchanges were able to be loaded. Exiting")
	}
}
