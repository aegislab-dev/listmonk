package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/captcha"
	"github.com/knadh/listmonk/internal/core"
	"github.com/labstack/echo/v4"
	null "gopkg.in/volatiletech/null.v6"
)

type serverConfig struct {
	SenderDomains []string `json:"sender_domains"`
	TestDomains   []string `json:"test_domains"`
	// The signed-in user's own address when it may receive a campaign test, or "".
	TestOwnAddress     string   `json:"test_own_address"`
	DefaultFrom        string   `json:"default_from"`
	DefaultReplyTo     string   `json:"default_reply_to"`
	FromAddresses      []string `json:"from_addresses"`
	FromDefault        *int     `json:"from_default"`
	ResolvedFrom       string   `json:"resolved_from"`
	RootURL            string   `json:"root_url"`
	FromEmail          string   `json:"from_email"`
	PublicSubscription struct {
		Enabled          bool        `json:"enabled"`
		CaptchaEnabled   bool        `json:"captcha_enabled"`
		CaptchaProvider  null.String `json:"captcha_provider"`
		CaptchaKey       null.String `json:"captcha_key"`
		AltchaComplexity int         `json:"altcha_complexity"`
		RedirectURLs     []string    `json:"redirect_urls"`
	} `json:"public_subscription"`
	Privacy struct {
		DisableTracking    bool `json:"disable_tracking"`
		IndividualTracking bool `json:"individual_tracking"`
	} `json:"privacy"`
	MediaProvider string          `json:"media_provider"`
	Messengers    []string        `json:"messengers"`
	Langs         []i18nLang      `json:"langs"`
	Lang          string          `json:"lang"`
	Permissions   json.RawMessage `json:"permissions"`
	Update        *AppUpdate      `json:"update"`
	NeedsRestart  bool            `json:"needs_restart"`
	HasLegacyUser bool            `json:"has_legacy_user"`
	Version       string          `json:"version"`

	// The column presets of the subscribers table and the default one.
	AxmColumnPresets       []core.ColumnPreset `json:"axm_column_presets"`
	AxmDefaultColumnPreset string              `json:"axm_default_column_preset"`

	// The standard attribute dictionary (keys, value formats, column name aliases).
	AxmStandardAttribs []core.StandardAttrib `json:"axm_standard_attribs"`
}

// columnPresetsForConfig returns the presets the screens need. When the database cannot be
// read, the built-in ones go out and the error is logged.
func (a *App) columnPresetsForConfig() []core.ColumnPreset {
	out, err := a.core.GetColumnPresets()
	if err != nil {
		a.log.Printf("error reading column presets: %v", err)
		out = append([]core.ColumnPreset{}, core.ColumnPresets...)
	}
	return out
}

// GetServerConfig returns general server config.
func (a *App) GetServerConfig(c echo.Context) error {
	out := serverConfig{
		SenderDomains:  append([]string{}, a.cfg.SenderDomains...),
		TestDomains:    append([]string{}, a.cfg.TestDomains...),
		TestOwnAddress: a.ownTestAddress(auth.GetUser(c)),
		DefaultFrom:    a.cfg.DefaultFrom,
		DefaultReplyTo: a.cfg.DefaultReplyTo,
		FromAddresses:  append([]string{}, a.cfg.FromAddresses...),
		FromDefault:    fromDefault(a.cfg),
		ResolvedFrom:   resolveFrom(auth.GetUser(c), a.cfg.DefaultFrom, a.cfg.FromEmail, a.cfg.SenderDomains),
		RootURL:        a.urlCfg.RootURL,
		FromEmail:      a.cfg.FromEmail,
		Lang:           a.cfg.Lang,
		Permissions:    a.cfg.PermissionsRaw,
		HasLegacyUser:  a.cfg.HasLegacyUser,

		AxmColumnPresets:       a.columnPresetsForConfig(),
		AxmDefaultColumnPreset: core.DefaultColumnPreset,
		AxmStandardAttribs:     core.StandardAttribs,
		Privacy: struct {
			DisableTracking    bool `json:"disable_tracking"`
			IndividualTracking bool `json:"individual_tracking"`
		}{
			DisableTracking:    a.cfg.Privacy.DisableTracking,
			IndividualTracking: a.cfg.Privacy.IndividualTracking,
		},
	}
	out.PublicSubscription.Enabled = a.cfg.EnablePublicSubPage
	for _, d := range a.cfg.Security.TrustedURLs {
		if d == "*" {
			continue
		}
		out.PublicSubscription.RedirectURLs = append(out.PublicSubscription.RedirectURLs, d)
	}

	// CAPTCHA.
	if a.cfg.Security.Captcha.Altcha.Enabled {
		out.PublicSubscription.CaptchaEnabled = true
		out.PublicSubscription.CaptchaProvider = null.StringFrom(captcha.ProviderAltcha)
		out.PublicSubscription.AltchaComplexity = a.cfg.Security.Captcha.Altcha.Complexity
	} else if a.cfg.Security.Captcha.HCaptcha.Enabled {
		out.PublicSubscription.CaptchaEnabled = true
		out.PublicSubscription.CaptchaProvider = null.StringFrom(captcha.ProviderHCaptcha)
		out.PublicSubscription.CaptchaKey = null.StringFrom(a.cfg.Security.Captcha.HCaptcha.Key)
	}

	out.MediaProvider = a.cfg.MediaUpload.Provider

	// Language list.
	langList, err := getI18nLangList(a.fs)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("Error loading language list: %v", err))
	}
	out.Langs = langList

	out.Messengers = make([]string, 0, len(a.messengers))
	for _, m := range a.messengers {
		out.Messengers = append(out.Messengers, m.Name())
	}

	a.Lock()
	out.NeedsRestart = a.needsRestart
	out.Update = a.update
	a.Unlock()
	out.Version = versionString

	return c.JSON(http.StatusOK, okResp{out})
}

// GetDashboardCharts returns chart data points to render ont he dashboard.
func (a *App) GetDashboardCharts(c echo.Context) error {
	// Get the chart data from the DB.
	out, err := a.core.GetDashboardCharts()
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// GetDashboardCounts returns stats counts to show on the dashboard.
func (a *App) GetDashboardCounts(c echo.Context) error {
	// Get the chart data from the DB.
	out, err := a.core.GetDashboardCounts()
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// ReloadApp sends a reload signal to the app, causing a full restart.
func (a *App) ReloadApp(c echo.Context) error {
	go func() {
		<-time.After(time.Millisecond * 500)

		// Send the reload signal to trigger the wait loop in main.
		a.chReload <- syscall.SIGHUP
	}()

	return c.JSON(http.StatusOK, okResp{true})
}

// fromDefault is the From list entry a new campaign starts with (null: no list).
func fromDefault(cfg *Config) *int {
	i := defaultFromIndex(cfg.FromEmail, cfg.FromAddresses)
	if i < 0 || len(cfg.SenderDomains) > 0 {
		return nil
	}
	return &i
}
