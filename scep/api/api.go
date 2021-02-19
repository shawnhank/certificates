package api

import (
	"context"
	"crypto"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/smallstep/certificates/acme"
	"github.com/smallstep/certificates/api"
	"github.com/smallstep/certificates/authority/provisioner"
	"github.com/smallstep/certificates/scep"

	microscep "github.com/micromdm/scep/scep"
)

const (
	opnGetCACert    = "GetCACert"
	opnGetCACaps    = "GetCACaps"
	opnPKIOperation = "PKIOperation"
)

// SCEPRequest is a SCEP server request.
type SCEPRequest struct {
	Operation string
	Message   []byte
}

// SCEPResponse is a SCEP server response.
type SCEPResponse struct {
	Operation string
	CACertNum int
	Data      []byte
	Err       error
}

// Handler is the SCEP request handler.
type Handler struct {
	Auth scep.Interface
}

// New returns a new SCEP API router.
func New(scepAuth scep.Interface) api.RouterHandler {
	return &Handler{scepAuth}
}

// Route traffic and implement the Router interface.
func (h *Handler) Route(r api.Router) {
	//getLink := h.Auth.GetLinkExplicit
	//fmt.Println(getLink)

	//r.MethodFunc("GET", "/bla", h.baseURLFromRequest(h.lookupProvisioner(nil)))
	//r.MethodFunc("GET", getLink(acme.NewNonceLink, "{provisionerID}", false, nil), h.baseURLFromRequest(h.lookupProvisioner(h.addNonce(h.GetNonce))))

	r.MethodFunc(http.MethodGet, "/", h.lookupProvisioner(h.Get))
	r.MethodFunc(http.MethodPost, "/", h.lookupProvisioner(h.Post))

}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {

	scepRequest, err := decodeSCEPRequest(r)
	if err != nil {
		fmt.Println(err)
		fmt.Println("not a scep get request")
		w.WriteHeader(500)
	}

	scepResponse := SCEPResponse{Operation: scepRequest.Operation}

	switch scepRequest.Operation {
	case opnGetCACert:
		err := h.GetCACert(w, r, scepResponse)
		if err != nil {
			fmt.Println(err)
		}

	case opnGetCACaps:
		err := h.GetCACaps(w, r, scepResponse)
		if err != nil {
			fmt.Println(err)
		}
	case opnPKIOperation:

	default:

	}
}

func (h *Handler) Post(w http.ResponseWriter, r *http.Request) {
	scepRequest, err := decodeSCEPRequest(r)
	if err != nil {
		fmt.Println(err)
		fmt.Println("not a scep post request")
		w.WriteHeader(500)
	}

	scepResponse := SCEPResponse{Operation: scepRequest.Operation}

	switch scepRequest.Operation {
	case opnPKIOperation:
		err := h.PKIOperation(w, r, scepRequest, scepResponse)
		if err != nil {
			fmt.Println(err)
		}
	default:

	}

}

const maxPayloadSize = 2 << 20

func decodeSCEPRequest(r *http.Request) (SCEPRequest, error) {

	defer r.Body.Close()

	method := r.Method
	query := r.URL.Query()

	var operation string
	if _, ok := query["operation"]; ok {
		operation = query.Get("operation")
	}

	switch method {
	case http.MethodGet:
		switch operation {
		case opnGetCACert, opnGetCACaps:
			return SCEPRequest{
				Operation: operation,
				Message:   []byte{},
			}, nil
		case opnPKIOperation:
			var message string
			if _, ok := query["message"]; ok {
				message = query.Get("message")
			}
			decodedMessage, err := base64.URLEncoding.DecodeString(message)
			if err != nil {
				return SCEPRequest{}, err
			}
			return SCEPRequest{
				Operation: operation,
				Message:   decodedMessage,
			}, nil
		default:
			return SCEPRequest{}, fmt.Errorf("unsupported operation: %s", operation)
		}
	case http.MethodPost:
		body, err := ioutil.ReadAll(io.LimitReader(r.Body, maxPayloadSize))
		if err != nil {
			return SCEPRequest{}, err
		}
		return SCEPRequest{
			Operation: operation,
			Message:   body,
		}, nil
	default:
		return SCEPRequest{}, fmt.Errorf("unsupported method: %s", method)
	}
}

type nextHTTP = func(http.ResponseWriter, *http.Request)

// lookupProvisioner loads the provisioner associated with the request.
// Responds 404 if the provisioner does not exist.
func (h *Handler) lookupProvisioner(next nextHTTP) nextHTTP {
	return func(w http.ResponseWriter, r *http.Request) {

		// name := chi.URLParam(r, "provisionerID")
		// provisionerID, err := url.PathUnescape(name)
		// if err != nil {
		// 	api.WriteError(w, fmt.Errorf("error url unescaping provisioner id '%s'", name))
		// 	return
		// }

		// TODO: make this configurable; and we might want to look at being able to provide multiple,
		// like the ACME one? The below assumes a SCEP provider (scep/) called "scep1" exists.
		provisionerID := "scep1"

		p, err := h.Auth.LoadProvisionerByID("scep/" + provisionerID)
		if err != nil {
			api.WriteError(w, err)
			return
		}

		scepProvisioner, ok := p.(*provisioner.SCEP)
		if !ok {
			api.WriteError(w, errors.New("provisioner must be of type SCEP"))
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, acme.ProvisionerContextKey, scep.Provisioner(scepProvisioner))
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) GetCACert(w http.ResponseWriter, r *http.Request, scepResponse SCEPResponse) error {

	certs, err := h.Auth.GetCACertificates()
	if err != nil {
		return err
	}

	if len(certs) == 0 {
		scepResponse.CACertNum = 0
		scepResponse.Err = errors.New("missing CA Cert")
	} else if len(certs) == 1 {
		scepResponse.Data = certs[0].Raw
		scepResponse.CACertNum = 1
	} else {
		data, err := microscep.DegenerateCertificates(certs)
		scepResponse.Data = data
		scepResponse.Err = err
	}

	return writeSCEPResponse(w, scepResponse)
}

func (h *Handler) GetCACaps(w http.ResponseWriter, r *http.Request, scepResponse SCEPResponse) error {

	ctx := r.Context()

	_, err := ProvisionerFromContext(ctx)
	if err != nil {
		return err
	}

	// TODO: get the actual capabilities from provisioner config
	scepResponse.Data = formatCapabilities(defaultCapabilities)

	return writeSCEPResponse(w, scepResponse)
}

func (h *Handler) PKIOperation(w http.ResponseWriter, r *http.Request, scepRequest SCEPRequest, scepResponse SCEPResponse) error {

	msg, err := microscep.ParsePKIMessage(scepRequest.Message)
	if err != nil {
		return err
	}

	certs, err := h.Auth.GetCACertificates()
	if err != nil {
		return err
	}

	// TODO: instead of getting the key to decrypt, add a decrypt function to the auth; less leaky
	key, err := h.Auth.GetSigningKey()
	if err != nil {
		return err
	}

	ca := certs[0]
	if err := msg.DecryptPKIEnvelope(ca, key); err != nil {
		return err
	}

	if msg.MessageType == microscep.PKCSReq {
		// TODO: CSR validation, like challenge password
	}

	csr := msg.CSRReqMessage.CSR
	id, err := createKeyIdentifier(csr.PublicKey)
	if err != nil {
		return err
	}

	serial := big.NewInt(int64(rand.Int63())) // TODO: serial logic?

	days := 40

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      csr.Subject,
		NotBefore:    time.Now().Add(-600).UTC(),
		NotAfter:     time.Now().AddDate(0, 0, days).UTC(),
		SubjectKeyId: id,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
		SignatureAlgorithm: csr.SignatureAlgorithm,
		EmailAddresses:     csr.EmailAddresses,
	}

	certRep, err := msg.SignCSR(ca, key, template)
	if err != nil {
		return err
	}

	//cert := certRep.CertRepMessage.Certificate
	//name := certName(cert)

	// TODO: check if CN already exists, if renewal is allowed and if existing should be revoked; fail if not
	// TODO: store the new cert for CN locally; should go into the DB

	scepResponse.Data = certRep.Raw

	api.LogCertificate(w, certRep.Certificate)

	return writeSCEPResponse(w, scepResponse)
}

func certName(cert *x509.Certificate) string {
	if cert.Subject.CommonName != "" {
		return cert.Subject.CommonName
	}
	return string(cert.Signature)
}

// createKeyIdentifier create an identifier for public keys
// according to the first method in RFC5280 section 4.2.1.2.
func createKeyIdentifier(pub crypto.PublicKey) ([]byte, error) {

	keyBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}

	id := sha1.Sum(keyBytes)

	return id[:], nil
}

func formatCapabilities(caps []string) []byte {
	return []byte(strings.Join(caps, "\n"))
}

// writeSCEPResponse writes a SCEP response back to the SCEP client.
func writeSCEPResponse(w http.ResponseWriter, response SCEPResponse) error {
	if response.Err != nil {
		http.Error(w, response.Err.Error(), http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", contentHeader(response.Operation, response.CACertNum))
	w.Write(response.Data)
	return nil
}

var (
	// TODO: check the default capabilities
	defaultCapabilities = []string{
		"Renewal",
		"SHA-1",
		"SHA-256",
		"AES",
		"DES3",
		"SCEPStandard",
		"POSTPKIOperation",
	}
)

const (
	certChainHeader = "application/x-x509-ca-ra-cert"
	leafHeader      = "application/x-x509-ca-cert"
	pkiOpHeader     = "application/x-pki-message"
)

func contentHeader(operation string, certNum int) string {
	switch operation {
	case opnGetCACert:
		if certNum > 1 {
			return certChainHeader
		}
		return leafHeader
	case opnPKIOperation:
		return pkiOpHeader
	default:
		return "text/plain"
	}
}

// ProvisionerFromContext searches the context for a provisioner. Returns the
// provisioner or an error.
func ProvisionerFromContext(ctx context.Context) (scep.Provisioner, error) {
	val := ctx.Value(acme.ProvisionerContextKey)
	if val == nil {
		return nil, errors.New("provisioner expected in request context")
	}
	pval, ok := val.(scep.Provisioner)
	if !ok || pval == nil {
		return nil, errors.New("provisioner in context is not a SCEP provisioner")
	}
	return pval, nil
}