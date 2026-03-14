package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"

	ggrpccreds "google.golang.org/grpc/credentials"
	ggrpcpeer "google.golang.org/grpc/peer"
)

func TestAuthorizeAdminClientFromContext_AllowsExpectedServiceAndRole(t *testing.T) {
	t.Parallel()

	ctx := contextWithPeerCert(&x509.Certificate{
		URIs: []*url.URL{
			mustParseProbeURL(t, "spiffe://aurora.local/service/aurora-admin"),
			mustParseProbeURL(t, "spiffe://aurora.local/role/control-plane"),
		},
	})

	if err := authorizeAdminClientFromContext(ctx, agentRPCMethodInstallModule, "", "aurora-admin", "control-plane"); err != nil {
		t.Fatalf("expected authorizeAdminClientFromContext to succeed, got: %v", err)
	}
}

func TestAuthorizeAdminClientFromContext_RejectsWrongService(t *testing.T) {
	t.Parallel()

	ctx := contextWithPeerCert(&x509.Certificate{
		URIs: []*url.URL{
			mustParseProbeURL(t, "spiffe://aurora.local/service/other-service"),
			mustParseProbeURL(t, "spiffe://aurora.local/role/control-plane"),
		},
	})

	if err := authorizeAdminClientFromContext(ctx, agentRPCMethodInstallModule, "", "aurora-admin", "control-plane"); err == nil {
		t.Fatalf("expected authorizeAdminClientFromContext to reject wrong service")
	}
}

func contextWithPeerCert(cert *x509.Certificate) context.Context {
	return ggrpcpeer.NewContext(context.Background(), &ggrpcpeer.Peer{
		AuthInfo: ggrpccreds.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{cert},
			},
		},
	})
}

func mustParseProbeURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse probe url failed: %v", err)
	}
	return parsed
}
