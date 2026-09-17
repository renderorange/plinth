package api

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func proxyRequest(w http.ResponseWriter, r *http.Request, host string, port int, path string) {
	target, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
	if err != nil {
		http.Error(w, "failed to parse proxy target URL", http.StatusInternalServerError)
		return
	}
	r.URL.Path = path
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.ServeHTTP(w, r)
}
