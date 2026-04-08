package jira

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// RFC3339 is jira datetime format.
	RFC3339 = "2006-01-02T15:04:05-0700"
	// RFC3339MilliLayout is jira datetime format with milliseconds.
	RFC3339MilliLayout = "2006-01-02T15:04:05.000-0700"

	// InstallationTypeCloud represents Jira cloud server.
	InstallationTypeCloud = "Cloud"
	// InstallationTypeLocal represents on-premise Jira servers.
	InstallationTypeLocal = "Local"

	baseURLv3 = "/rest/api/3"
	baseURLv2 = "/rest/api/2"
	baseURLv1 = "/rest/agile/1.0"

	apiVersion2 = "v2"
	apiVersion3 = "v3"

	browserUserAgent        = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36"
	cookieRetryAttemptsMax = 4
)

var (
	// ErrNoResult denotes no results.
	ErrNoResult = fmt.Errorf("jira: no result")
	// ErrEmptyResponse denotes empty response from the server.
	ErrEmptyResponse = fmt.Errorf("jira: empty response from server")
)

// ErrUnexpectedResponse denotes response code other than the expected one.
type ErrUnexpectedResponse struct {
	Body       Errors
	Status     string
	StatusCode int
	Username   string
	LoginReason string
	AuthRealm  string
}

func (e *ErrUnexpectedResponse) Error() string {
	msg := strings.TrimSpace(e.Body.String())
	if msg != "" {
		return msg
	}

	parts := []string{fmt.Sprintf("jira: unexpected response %d", e.StatusCode)}
	if e.Status != "" {
		parts[0] = fmt.Sprintf("jira: unexpected response %s", strings.TrimSpace(e.Status))
	}
	if e.Username != "" {
		parts = append(parts, fmt.Sprintf("user=%s", e.Username))
	}
	if e.LoginReason != "" {
		parts = append(parts, fmt.Sprintf("login_reason=%s", e.LoginReason))
	}
	if e.AuthRealm != "" {
		parts = append(parts, fmt.Sprintf("auth=%s", e.AuthRealm))
	}

	if e.StatusCode == http.StatusUnauthorized && strings.EqualFold(e.Username, "anonymous") {
		parts = append(parts, "browser session missing or expired")
	}

	return strings.Join(parts, " | ")
}

// ErrMultipleFailed represents a grouped error, usually when
// multiple request fails when running them in a loop.
type ErrMultipleFailed struct {
	Msg string
}

func (e *ErrMultipleFailed) Error() string {
	return e.Msg
}

// Errors is a jira error type.
type Errors struct {
	Errors          map[string]string
	ErrorMessages   []string
	WarningMessages []string
}

func (e Errors) String() string {
	var out strings.Builder

	if len(e.ErrorMessages) > 0 || len(e.Errors) > 0 {
		out.WriteString("\nError:\n")
		for _, v := range e.ErrorMessages {
			out.WriteString(fmt.Sprintf("  - %s\n", v))
		}
		for k, v := range e.Errors {
			out.WriteString(fmt.Sprintf("  - %s: %s\n", k, v))
		}
	}

	if len(e.WarningMessages) > 0 {
		out.WriteString("\nWarning:\n")
		for _, v := range e.WarningMessages {
			out.WriteString(fmt.Sprintf("  - %s\n", v))
		}
	}

	return out.String()
}

// Header is a key, value pair for request headers.
type Header map[string]string

// MTLSConfig is MTLS authtype specific config.
type MTLSConfig struct {
	CaCert     string
	ClientCert string
	ClientKey  string
}

// Config is a jira config.
type Config struct {
	Server     string
	Login      string
	APIToken   string
	AuthType   *AuthType
	Insecure   *bool
	Debug      bool
	MTLSConfig MTLSConfig
}

// Client is a jira client.
type Client struct {
	transport http.RoundTripper
	jar       http.CookieJar
	insecure  bool
	server    string
	login     string
	authType  *AuthType
	token     string
	timeout   time.Duration
	debug     bool
}

// ClientFunc decorates option for client.
type ClientFunc func(*Client)

// NewClient instantiates new jira client.
func NewClient(c Config, opts ...ClientFunc) *Client {
	client := Client{
		server:   strings.TrimSuffix(c.Server, "/"),
		login:    c.Login,
		token:    NormalizeCookieToken(c.APIToken),
		authType: c.AuthType,
		debug:    c.Debug,
	}

	for _, opt := range opts {
		opt(&client)
	}

	if c.AuthType != nil && *c.AuthType == AuthTypeCookie {
		client.jar = newCookieJar(client.server, client.token)
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: client.insecure,
		},
		DialContext: (&net.Dialer{
			Timeout: client.timeout,
		}).DialContext,
	}

	if c.AuthType != nil && *c.AuthType == AuthTypeMTLS {
		// Create a CA certificate pool and add cert.pem to it.
		caCert, err := os.ReadFile(c.MTLSConfig.CaCert)
		if err != nil {
			log.Fatalf("%s, %s", err, c.MTLSConfig.CaCert)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)

		// Read the key pair to create the certificate.
		cert, err := tls.LoadX509KeyPair(c.MTLSConfig.ClientCert, c.MTLSConfig.ClientKey)
		if err != nil {
			log.Fatal(err)
		}

		// Add the MTLS specific configuration.
		transport.TLSClientConfig.RootCAs = caCertPool
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
		transport.TLSClientConfig.Renegotiation = tls.RenegotiateFreelyAsClient
	}

	client.transport = transport

	return &client
}

// WithTimeout is a functional opt to attach timeout to the client.
func WithTimeout(to time.Duration) ClientFunc {
	return func(c *Client) {
		c.timeout = to
	}
}

// WithInsecureTLS is a functional opt that allow you to skip TLS certificate verification.
func WithInsecureTLS(ins bool) ClientFunc {
	return func(c *Client) {
		c.insecure = ins
	}
}

// Get sends GET request to v3 version of the jira api.
func (c *Client) Get(ctx context.Context, path string, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodGet, c.server+baseURLv3+path, nil, headers)
}

// GetV2 sends GET request to v2 version of the jira api.
func (c *Client) GetV2(ctx context.Context, path string, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodGet, c.server+baseURLv2+path, nil, headers)
}

// GetV1 sends get request to v1 version of the jira api.
func (c *Client) GetV1(ctx context.Context, path string, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodGet, c.server+baseURLv1+path, nil, headers)
}

// Post sends POST request to v3 version of the jira api.
func (c *Client) Post(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPost, c.server+baseURLv3+path, body, headers)
}

// PostV2 sends POST request to v2 version of the jira api.
func (c *Client) PostV2(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPost, c.server+baseURLv2+path, body, headers)
}

// PostV1 sends POST request to v1 version of the jira api.
func (c *Client) PostV1(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPost, c.server+baseURLv1+path, body, headers)
}

// Put sends PUT request to v3 version of the jira api.
func (c *Client) Put(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPut, c.server+baseURLv3+path, body, headers)
}

// PutV2 sends PUT request to v2 version of the jira api.
func (c *Client) PutV2(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPut, c.server+baseURLv2+path, body, headers)
}

// PutV1 sends PUT request to v1 version of the jira api.
func (c *Client) PutV1(ctx context.Context, path string, body []byte, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodPut, c.server+baseURLv1+path, body, headers)
}

// DeleteV2 sends DELETE request to v2 version of the jira api.
func (c *Client) DeleteV2(ctx context.Context, path string, headers Header) (*http.Response, error) {
	return c.request(ctx, http.MethodDelete, c.server+baseURLv2+path, nil, headers)
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte, headers Header) (*http.Response, error) {
	var (
		req *http.Request
		res *http.Response
		err error
	)


	// Set default auth type to `basic`.
	if c.authType == nil {
		basic := AuthTypeBasic
		c.authType = &basic
	}

	req, err = c.buildRequest(method, endpoint, body, headers)
	if err != nil {
		return nil, err
	}

	defer func() {
		if c.debug {
			dump(req, res)
		}
	}()

	httpClient := &http.Client{Transport: c.transport, Jar: c.jar}

	res, err = httpClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	if c.authType.String() == string(AuthTypeCookie) && method == http.MethodGet && !strings.HasSuffix(endpoint, baseURLv2+"/serverInfo") {
		for attempt := 0; attempt < cookieRetryAttemptsMax && res.StatusCode == http.StatusUnauthorized; attempt++ {
			_ = res.Body.Close()

			_ = c.warmupCookieSession(ctx, httpClient, endpoint)

			req, err = c.buildRequest(method, endpoint, body, headers)
			if err != nil {
				return nil, err
			}

			res, err = httpClient.Do(req.WithContext(ctx))
			if err != nil {
				return nil, err
			}
		}
	}

	return res, nil
}

func (c *Client) buildRequest(method, target string, body []byte, headers Header) (*http.Request, error) {
	r, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		r.Header.Set(k, v)
	}

	if c.authType != nil && c.authType.String() == string(AuthTypeCookie) {
		if r.Header.Get("User-Agent") == "" {
			r.Header.Set("User-Agent", browserUserAgent)
		}
		if r.Header.Get("Accept") == "" {
			r.Header.Set("Accept", cookieAuthAccept(target))
		}
		if r.Header.Get("Referer") == "" {
			r.Header.Set("Referer", cookieAuthReferer(c.server, target))
		}
	}

	switch c.authType.String() {
	case string(AuthTypeMTLS):
		if c.token != "" {
			r.Header.Add("Authorization", "Bearer "+c.token)
		}
	case string(AuthTypeBearer):
		r.Header.Add("Authorization", "Bearer "+c.token)
	case string(AuthTypeCookie):
	case string(AuthTypeBasic):
		r.SetBasicAuth(c.login, c.token)
	}

	return r, nil
}

func cookieAuthAccept(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return "*/*"
	}

	if strings.HasPrefix(u.Path, "/browse/") || strings.Contains(u.Path, "/RapidBoard.jspa") || u.Path == "/" {
		return "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	}

	return "*/*"
}

func newCookieJar(server, token string) http.CookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}

	u, err := url.Parse(server)
	if err != nil {
		return jar
	}

	var cookies []*http.Cookie
	token = NormalizeCookieToken(token)

	if strings.Contains(token, "=") {
		for _, part := range strings.Split(token, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}

			cookies = append(cookies, &http.Cookie{
				Name:  strings.TrimSpace(kv[0]),
				Value: strings.TrimSpace(kv[1]),
				Path:  "/",
			})
		}
	} else if token != "" {
		cookies = append(cookies, &http.Cookie{
			Name:  "JSESSIONID",
			Value: token,
			Path:  "/",
		})
	}

	jar.SetCookies(u, cookies)

	return jar
}

func cookieAuthReferer(server, target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return server + "/"
	}

	const (
		issuePathV2 = "/rest/api/2/issue/"
		issuePathV3 = "/rest/api/3/issue/"
	)

	path := u.Path
	idx := strings.Index(path, issuePathV2)
	baseLen := len(issuePathV2)
	if idx == -1 {
		idx = strings.Index(path, issuePathV3)
		baseLen = len(issuePathV3)
	}

	if idx != -1 {
		key := path[idx+baseLen:]
		if cut := strings.Index(key, "/"); cut != -1 {
			key = key[:cut]
		}
		if key != "" {
			return server + "/browse/" + key
		}
	}

	if boardID := boardIDFromTarget(target); boardID != "" {
		return server + "/secure/RapidBoard.jspa?rapidView=" + boardID + "&view=planning&issueLimit=100"
	}

	return server + "/"
}

func dump(req *http.Request, res *http.Response) {
	reqDump, _ := httputil.DumpRequest(req, true)
	prettyPrintDump("Request Details", reqDump)

	if res != nil {
		respDump, _ := httputil.DumpResponse(res, false)
		prettyPrintDump("Response Details", respDump)
	}
}

func prettyPrintDump(heading string, data []byte) {
	const separatorWidth = 60

	fmt.Printf("\n\n%s", strings.ToUpper(heading))
	fmt.Printf("\n%s\n\n", strings.Repeat("-", separatorWidth))
	fmt.Print(string(data))
}

func formatUnexpectedResponse(res *http.Response) *ErrUnexpectedResponse {
	var b Errors

	// We don't care about decoding error here.
	_ = json.NewDecoder(res.Body).Decode(&b)

	return &ErrUnexpectedResponse{
		Body:       b,
		Status:     res.Status,
		StatusCode: res.StatusCode,
		Username:   res.Header.Get("X-AUSERNAME"),
		LoginReason: res.Header.Get("X-Seraph-LoginReason"),
		AuthRealm:  res.Header.Get("WWW-Authenticate"),
	}
}
