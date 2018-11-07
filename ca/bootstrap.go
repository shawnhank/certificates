package ca

import (
	"context"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"github.com/smallstep/cli/jose"
	"gopkg.in/square/go-jose.v2/jwt"
)

type tokenClaims struct {
	SHA string `json:"sha"`
	jose.Claims
}

// Bootstrap is a helper function that initializes a client with the
// configuration in the bootstrap token.
func Bootstrap(token string) (*Client, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing token")
	}
	var claims tokenClaims
	if err := tok.UnsafeClaimsWithoutVerification(&claims); err != nil {
		return nil, errors.Wrap(err, "error parsing token")
	}

	// Validate bootstrap token
	switch {
	case len(claims.SHA) == 0:
		return nil, errors.New("invalid bootstrap token: sha claim is not present")
	case !strings.HasPrefix(strings.ToLower(claims.Audience[0]), "http"):
		return nil, errors.New("invalid bootstrap token: aud claim is not a url")
	}

	return NewClient(claims.Audience[0], WithRootSHA256(claims.SHA))
}

// BootstrapServer is a helper function that using the given token returns the
// given http.Server configured with a TLS certificate signed by the Certificate
// Authority. By default the server will kick off a routine that will renew the
// certificate after 2/3rd of the certificate's lifetime has expired.
//
// Usage:
//   // Default example with certificate rotation.
//   srv, err := ca.BootstrapServer(context.Background(), token, &http.Server{
//       Addr: ":443",
//       Handler: handler,
//   })
//
//   // Example canceling automatic certificate rotation.
//   ctx, cancel := context.WithCancel(context.Background())
//   defer cancel()
//   srv, err := ca.BootstrapServer(ctx, token, &http.Server{
//       Addr: ":443",
//       Handler: handler,
//   })
//   if err != nil {
//       return err
//   }
//   srv.ListenAndServeTLS("", "")
func BootstrapServer(ctx context.Context, token string, base *http.Server) (*http.Server, error) {
	if base.TLSConfig != nil {
		return nil, errors.New("server TLSConfig is already set")
	}

	client, err := Bootstrap(token)
	if err != nil {
		return nil, err
	}

	req, pk, err := CreateSignRequest(token)
	if err != nil {
		return nil, err
	}

	sign, err := client.Sign(req)
	if err != nil {
		return nil, err
	}

	tlsConfig, err := client.GetServerTLSConfig(ctx, sign, pk)
	if err != nil {
		return nil, err
	}

	base.TLSConfig = tlsConfig
	return base, nil
}

// BootstrapClient is a helper function that using the given bootstrap token
// return an http.Client configured with a Transport prepared to do TLS
// connections using the client certificate returned by the certificate
// authority. By default the server will kick off a routine that will renew the
// certificate after 2/3rd of the certificate's lifetime has expired.
//
// Usage:
//   // Default example with certificate rotation.
//   client, err := ca.BootstrapClient(ctx.Background(), token)
//
//   // Example canceling automatic certificate rotation.
//   ctx, cancel := context.WithCancel(context.Background())
//   defer cancel()
//   client, err := ca.BootstrapClient(ctx, token)
//   if err != nil {
//     return err
//   }
//   resp, err := client.Get("https://internal.smallstep.com")
func BootstrapClient(ctx context.Context, token string) (*http.Client, error) {
	client, err := Bootstrap(token)
	if err != nil {
		return nil, err
	}

	req, pk, err := CreateSignRequest(token)
	if err != nil {
		return nil, err
	}

	sign, err := client.Sign(req)
	if err != nil {
		return nil, err
	}

	transport, err := client.Transport(ctx, sign, pk)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: transport,
	}, nil
}