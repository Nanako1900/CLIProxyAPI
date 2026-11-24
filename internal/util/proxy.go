// Package util provides utility functions for the CLI Proxy API server.
// It includes helper functions for proxy configuration, HTTP client setup,
// log level management, and other common operations used across the application.
package util

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

type failingProxyRoundTripper struct {
	err error
}

func (f failingProxyRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, f.err
}

// SetProxy configures the provided HTTP client with proxy settings from the configuration.
// It supports SOCKS5, HTTP, and HTTPS proxies. The function modifies the client's transport
// to route requests through the configured proxy server.
// If an explicit proxy URL is configured but invalid, SetProxy installs a fail-closed
// transport rather than silently falling back to a direct connection.
func SetProxy(cfg *config.SDKConfig, httpClient *http.Client) *http.Client {
	if cfg == nil || httpClient == nil {
		return httpClient
	}

	proxyURL := strings.TrimSpace(cfg.ProxyURL)
	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
		log.Errorf("%v", errBuild)
		if proxyURL != "" {
			httpClient.Transport = failingProxyRoundTripper{
				err: fmt.Errorf("invalid proxy configuration: %w", errBuild),
			}
		}
		return httpClient
	}
	if transport != nil {
		httpClient.Transport = transport
	}
	return httpClient
}
