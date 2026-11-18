package management

import (
	"io"
	"net/http"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// CheckOAuthProxyIP checks the proposed account route without creating an OAuth session.
func (h *Handler) CheckOAuthProxyIP(c *gin.Context) {
	checkOAuthProxyIP(c, "https://api64.ipify.org")
}

func checkOAuthProxyIP(c *gin.Context, serviceURL string) {
	c.Header("Cache-Control", "no-store")
	var body struct {
		ProxyURL string `json:"proxy_url"`
	}
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	proxyURL, errParse := parseNewOAuthSOCKSProxy(body.ProxyURL)
	if errParse != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errParse.Error()})
		return
	}

	// A fresh transport uses only this SOCKS route. RoundTrip neither follows
	// redirects nor forwards headers (including the incoming management key).
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	const failure = "could not verify the exit IP through the supplied SOCKS proxy"
	req, errReq := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, serviceURL, nil)
	if errReq != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": failure})
		return
	}
	resp, errSend := transport.RoundTrip(req)
	if errSend != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": failure})
		return
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Debug("failed to close OAuth proxy IP check response")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": failure})
		return
	}
	const maxBytes = 64 // One plain-text IPv4 or IPv6 address, optionally followed by a newline.
	data, errRead := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	ip, errIP := netip.ParseAddr(strings.TrimSpace(string(data)))
	ip = ip.Unmap()
	if errRead != nil || len(data) > maxBytes || errIP != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		c.JSON(http.StatusBadGateway, gin.H{"error": failure})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ip": ip.String()})
}
