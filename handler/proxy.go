package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type mediaContentRequest struct {
	URL            string `json:"url"`
	APIKey         string `json:"apiKey"`
	CredentialType string `json:"credentialType"`
}

var mediaClient = newMediaClient()

var nonPublicMediaNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var publicMediaIPv6 = netip.MustParsePrefix("2000::/3")
var errNonPublicMediaAddress = errors.New("media destination is not publicly routable")

func isPublicMediaIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Is6() && !publicMediaIPv6.Contains(ip) {
		return false
	}
	for _, network := range nonPublicMediaNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicMedia(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range addresses {
		if !isPublicMediaIP(ip) {
			return nil, errNonPublicMediaAddress
		}
	}

	var dialer net.Dialer
	var dialErr error = errNonPublicMediaAddress
	for _, ip := range addresses {
		// Dial the validated address directly while HTTP and TLS retain the original hostname.
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, dialErr
}

func newMediaClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialPublicMedia
	return &http.Client{
		Transport: transport,
		Timeout:   aiUpstreamTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many media redirects")
			}
			if req.URL.User != nil || req.URL.Hostname() == "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
				return errors.New("unsupported media redirect")
			}
			if req.URL.Scheme != via[0].URL.Scheme || !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				req.Header.Del("Authorization")
				req.Header.Del("X-Goog-Api-Key")
				req.Header.Del("Referer")
			}
			return nil
		},
	}
}

// MediaContent 代下载浏览器直接取不到的媒体内容：需要 Authorization 的视频地址，
// 以及没有 CORS 头的图片外链（预签名 URL 不能带 Authorization，所以 apiKey 可选）。
func MediaContent(w http.ResponseWriter, r *http.Request) {
	serveMediaContent(w, r, mediaClient)
}

func serveMediaContent(w http.ResponseWriter, r *http.Request, client *http.Client) {
	var payload mediaContentRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		FailStatus(w, http.StatusBadRequest, "媒体下载请求无效")
		return
	}
	target := strings.TrimSpace(payload.URL)
	apiKey := strings.TrimSpace(payload.APIKey)
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		FailStatus(w, http.StatusBadRequest, "媒体下载地址无效")
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil || request.URL.User != nil || request.URL.Hostname() == "" {
		FailStatus(w, http.StatusBadRequest, "媒体下载地址无效")
		return
	}
	if apiKey != "" {
		if payload.CredentialType == "gemini" {
			request.Header.Set("X-Goog-Api-Key", apiKey)
		} else {
			request.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	response, err := client.Do(request)
	if errors.Is(err, errNonPublicMediaAddress) {
		FailStatus(w, http.StatusBadRequest, "媒体下载仅支持公网地址")
		return
	}
	if err != nil {
		FailStatus(w, http.StatusBadGateway, "媒体下载失败，请稍后重试")
		return
	}
	defer response.Body.Close()

	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType == "image/svg+xml" || !strings.HasPrefix(contentType, "image/") &&
		!strings.HasPrefix(contentType, "audio/") && !strings.HasPrefix(contentType, "video/") {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	for _, key := range []string{"Content-Disposition", "Content-Length"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
