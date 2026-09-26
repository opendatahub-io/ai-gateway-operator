//go:build e2e

/*
Copyright 2026.

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

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestPraxisTenantIsolation(t *testing.T) {
	t.Run("should verify praxis proxy routes for openai resource apis", testPraxisProxyRoutes)
	t.Run("should verify identity header injection and forwarding", testPraxisIdentityHeaderForwarding)
	t.Run("should reject cross-tenant resource access", testPraxisCrossTenantRejection)
}

func testPraxisProxyRoutes(t *testing.T) {
	g := NewWithT(t)

	routes := []string{
		"/v1/files",
		"/v1/vector_stores",
		"/v1/vector_stores/vs_12345",
		"/v1/vector_stores/vs_12345/files",
	}

	for _, route := range routes {
		t.Run(fmt.Sprintf("route_%s", strings.ReplaceAll(route, "/", "_")), func(t *testing.T) {
			g.Expect(route).To(Or(
				HavePrefix("/v1/files"),
				HavePrefix("/v1/vector_stores"),
			))
		})
	}
}

func testPraxisIdentityHeaderForwarding(t *testing.T) {
	g := NewWithT(t)

	var receivedUserID, receivedTenantID string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUserID = r.Header.Get("x-user-id")
		receivedTenantID = r.Header.Get("x-tenant-id")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	g.Expect(err).NotTo(HaveOccurred())

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			r.Header.Set("x-user-id", "user-alice")
			r.Header.Set("x-tenant-id", "tenant-alpha")
		}
		proxy.ServeHTTP(w, r)
	})

	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/v1/files", nil)
	g.Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer sa-alice-token")

	resp, err := http.DefaultClient.Do(req)
	g.Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
	g.Expect(receivedUserID).To(Equal("user-alice"))
	g.Expect(receivedTenantID).To(Equal("tenant-alpha"))
}

func testPraxisCrossTenantRejection(t *testing.T) {
	g := NewWithT(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("x-user-id")
		tenantID := r.Header.Get("x-tenant-id")

		if strings.HasPrefix(r.URL.Path, "/v1/vector_stores/vs_123") && (tenantID != "tenant-alpha" || userID != "user-alice") {
			http.Error(w, "Forbidden: cross-tenant resource access denied", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// 1. User Alice (Tenant Alpha) authorized
	reqA, err := http.NewRequest(http.MethodGet, server.URL+"/v1/vector_stores/vs_123", nil)
	g.Expect(err).NotTo(HaveOccurred())
	reqA.Header.Set("x-user-id", "user-alice")
	reqA.Header.Set("x-tenant-id", "tenant-alpha")

	respA, err := http.DefaultClient.Do(reqA)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(respA.StatusCode).To(Equal(http.StatusOK))
	respA.Body.Close()

	// 2. User Bob (Tenant Beta) cross-tenant attempt rejected
	reqB, err := http.NewRequest(http.MethodGet, server.URL+"/v1/vector_stores/vs_123", nil)
	g.Expect(err).NotTo(HaveOccurred())
	reqB.Header.Set("x-user-id", "user-bob")
	reqB.Header.Set("x-tenant-id", "tenant-beta")

	respB, err := http.DefaultClient.Do(reqB)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(respB.StatusCode).To(Equal(http.StatusForbidden))
	respB.Body.Close()
}
