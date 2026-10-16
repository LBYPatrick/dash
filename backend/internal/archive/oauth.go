package archive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type OAuthEndpoints struct{ Device, Token string }

func parkedRequest(parent context.Context, state func() string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if state() != "parked" {
		cancel()
		return ctx, cancel
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if state() != "parked" {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func oauthEndpoints(c Config) OAuthEndpoints {
	if c.Kind == "gdrive" {
		return OAuthEndpoints{"https://oauth2.googleapis.com/device/code", "https://oauth2.googleapis.com/token"}
	}
	tenant := c.Tenant
	if tenant == "" {
		tenant = "common"
	}
	base := "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/"
	return OAuthEndpoints{base + "devicecode", base + "token"}
}
func providerHTTP() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || request.URL.Scheme != "https" {
			return errors.New("provider redirect rejected")
		}
		if request.URL.Host != via[0].URL.Host {
			request.Header.Del("Authorization")
		}
		return nil
	}}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func tokenRequest(ctx context.Context, client *http.Client, endpoint string, values url.Values) (tokenResponse, error) {
	var token tokenResponse
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return token, errors.New("invalid authorization endpoint")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return token, RemoteError{"OAuth", "connect", 0}
	}
	defer response.Body.Close()
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&token); err != nil {
		return token, RemoteError{"OAuth", "decode", response.StatusCode}
	}
	if response.StatusCode != 200 && token.Error == "" {
		return token, RemoteError{"OAuth", "authorize", response.StatusCode}
	}
	return token, nil
}
func accessToken(ctx context.Context, c Config, configs *Configs, client *http.Client, endpoints OAuthEndpoints) (string, error) {
	if c.Secret.AccessToken != "" && (c.Secret.ExpiresAt == 0 || c.Secret.ExpiresAt > time.Now().Unix()+60) {
		return c.Secret.AccessToken, nil
	}
	if c.Secret.RefreshToken == "" {
		return "", ErrReconnect
	}
	values := url.Values{"client_id": {c.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {c.Secret.RefreshToken}}
	if c.Secret.ClientSecret != "" {
		values.Set("client_secret", c.Secret.ClientSecret)
	}
	if c.Kind == "onedrive" {
		values.Set("scope", "offline_access Files.ReadWrite")
	}
	token, err := tokenRequest(ctx, client, endpoints.Token, values)
	if err != nil {
		return "", err
	}
	if token.Error != "" || token.AccessToken == "" {
		return "", ErrReconnect
	}
	secret := c.Secret
	secret.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		secret.RefreshToken = token.RefreshToken
	}
	secret.ExpiresAt = time.Now().Unix() + token.ExpiresIn
	if err = configs.UpdateSecret(c, secret); err != nil {
		return "", err
	}
	return secret.AccessToken, nil
}

type Authorization struct {
	DestinationID   string `json:"destinationId"`
	Status          string `json:"status"`
	UserCode        string `json:"userCode,omitempty"`
	VerificationURI string `json:"verificationUri,omitempty"`
	ExpiresAt       int64  `json:"expiresAt,omitempty"`
}
type pendingAuthorization struct {
	public     Authorization
	deviceCode string
	interval   time.Duration
	config     Config
}
type Authorizer struct {
	ctx       context.Context
	configs   *Configs
	state     func() string
	client    *http.Client
	endpoints func(Config) OAuthEndpoints
	mu        sync.Mutex
	pending   map[string]*pendingAuthorization
}

func NewAuthorizer(ctx context.Context, configs *Configs, state func() string) *Authorizer {
	return &Authorizer{ctx: ctx, configs: configs, state: state, client: providerHTTP(), endpoints: oauthEndpoints, pending: map[string]*pendingAuthorization{}}
}
func (a *Authorizer) Snapshot() []Authorization {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := []Authorization{}
	for _, pending := range a.pending {
		public := pending.public
		if public.Status == "waiting" && a.state() != "parked" {
			public.Status = "waiting_parked"
		}
		result = append(result, public)
	}
	return result
}
func (a *Authorizer) Cancel(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, id)
}
func (a *Authorizer) Begin(ctx context.Context, id string) (Authorization, error) {
	if a.state() != "parked" {
		return Authorization{}, errors.New("authorization requires confirmed parked state")
	}
	ctx, cancel := parkedRequest(ctx, a.state)
	defer cancel()
	c, err := a.configs.Load(id)
	if err != nil {
		return Authorization{}, err
	}
	if c.Kind != "gdrive" && c.Kind != "onedrive" {
		return Authorization{}, errors.New("this destination does not use provider authorization")
	}
	a.mu.Lock()
	if pending := a.pending[id]; pending != nil && pending.public.Status == "waiting" && pending.public.ExpiresAt > time.Now().Unix() {
		public := pending.public
		a.mu.Unlock()
		return public, nil
	}
	a.mu.Unlock()
	scope := "https://www.googleapis.com/auth/drive.file"
	if c.Kind == "onedrive" {
		scope = "offline_access Files.ReadWrite"
	}
	request, err := http.NewRequestWithContext(ctx, "POST", a.endpoints(c).Device, strings.NewReader(url.Values{"client_id": {c.ClientID}, "scope": {scope}}.Encode()))
	if err != nil {
		return Authorization{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.client.Do(request)
	if err != nil {
		return Authorization{}, RemoteError{"OAuth", "start authorization", 0}
	}
	defer response.Body.Close()
	var value struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		VerificationURL string `json:"verification_url"`
		ExpiresIn       int64  `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if response.StatusCode != 200 {
		return Authorization{}, RemoteError{"OAuth", "start authorization", response.StatusCode}
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&value); err != nil {
		return Authorization{}, errors.New("invalid device authorization response")
	}
	if value.VerificationURI == "" {
		value.VerificationURI = value.VerificationURL
	}
	uri, err := url.Parse(value.VerificationURI)
	if err != nil || uri.Scheme != "https" || uri.Hostname() == "" || value.DeviceCode == "" || value.UserCode == "" || value.ExpiresIn <= 0 || value.ExpiresIn > 3600 {
		return Authorization{}, errors.New("invalid device authorization response")
	}
	pending := &pendingAuthorization{public: Authorization{DestinationID: id, Status: "waiting", UserCode: value.UserCode, VerificationURI: value.VerificationURI, ExpiresAt: time.Now().Unix() + value.ExpiresIn}, deviceCode: value.DeviceCode, interval: time.Duration(max(5, value.Interval)) * time.Second, config: c}
	a.mu.Lock()
	a.pending[id] = pending
	public := pending.public
	a.mu.Unlock()
	go a.poll(pending)
	return public, nil
}
func (a *Authorizer) poll(pending *pendingAuthorization) {
	for {
		timer := time.NewTimer(pending.interval)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		a.mu.Lock()
		current := a.pending[pending.public.DestinationID] == pending
		expired := time.Now().Unix() >= pending.public.ExpiresAt
		if expired {
			pending.public.Status = "expired"
			pending.deviceCode = ""
		}
		a.mu.Unlock()
		if !current || expired {
			return
		}
		if a.state() != "parked" {
			continue
		}
		c := pending.config
		values := url.Values{"client_id": {c.ClientID}, "device_code": {pending.deviceCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
		if c.Secret.ClientSecret != "" {
			values.Set("client_secret", c.Secret.ClientSecret)
		}
		limited, timeout := context.WithTimeout(a.ctx, 20*time.Second)
		ctx, cancel := parkedRequest(limited, a.state)
		token, err := tokenRequest(ctx, a.client, a.endpoints(c).Token, values)
		cancel()
		timeout()
		if err != nil {
			continue
		}
		if token.Error == "authorization_pending" {
			continue
		}
		if token.Error == "slow_down" {
			pending.interval = min(60*time.Second, pending.interval+5*time.Second)
			continue
		}
		a.mu.Lock()
		if a.pending[c.ID] != pending {
			a.mu.Unlock()
			return
		}
		status := "failed"
		if token.Error == "" && token.AccessToken != "" {
			secret := c.Secret
			secret.AccessToken, secret.RefreshToken, secret.ExpiresAt = token.AccessToken, token.RefreshToken, time.Now().Unix()+token.ExpiresIn
			if err = a.configs.UpdateSecret(c, secret); err == nil {
				status = "connected"
			}
		}
		pending.public.Status = status
		pending.public.UserCode = ""
		pending.deviceCode = ""
		a.mu.Unlock()
		return
	}
}
