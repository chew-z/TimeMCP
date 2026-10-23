package oauth

import (
	"crypto/subtle"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// pendingAuthorizationTTL is how long a login form stays redeemable. It is
// longer than an authorization code's life because a person has to type a
// password inside it.
const pendingAuthorizationTTL = 10 * time.Minute

// maxConcurrentLogins caps password verification because PBKDF2 at 600k
// iterations is CPU-bound; unbounded concurrent logins are a CPU exhaustion
// vector, while this cap bounds verification regardless of how many IPs attack.
const maxConcurrentLogins = 4

// AuthorizationService issues authorization codes after a password login.
type AuthorizationService struct {
	cfg      Config
	store    *Store
	registry *ClientRegistry
	log      *slog.Logger
	// verifySlots bounds concurrent password verification.
	verifySlots chan struct{}
	// verifyPassword verifies a stored password hash; tests replace it.
	verifyPassword func(stored, password string) bool
}

// NewAuthorizationService wires the authorize and login endpoints.
func NewAuthorizationService(cfg Config, store *Store, registry *ClientRegistry, log *slog.Logger) *AuthorizationService {
	if log == nil {
		log = slog.Default()
	}
	return &AuthorizationService{
		cfg:            cfg,
		store:          store,
		registry:       registry,
		log:            log,
		verifySlots:    make(chan struct{}, maxConcurrentLogins),
		verifyPassword: verifyPassword,
	}
}

// authorizeRequest is a parsed and validated authorization request.
type authorizeRequest struct {
	client        *Client
	redirectURI   string
	state         string
	codeChallenge string
	resource      string
	scope         string
}

// AuthorizeHandler serves GET /authorize.
func (s *AuthorizationService) AuthorizeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.renderError(w, http.StatusMethodNotAllowed, "authorization requires GET")
			return
		}
		if !s.store.AllowRequest("authorize", clientIP(r)) {
			s.renderError(w, http.StatusTooManyRequests, "Too many authorization attempts. Wait a moment and try again.")
			return
		}

		query := r.URL.Query()
		clientID := query.Get("client_id")
		redirectURI := query.Get("redirect_uri")

		// RFC 6749 section 4.1.2.1: while client_id or redirect_uri is
		// unvalidated there is no trustworthy place to redirect to, so these
		// two failures render a page instead. Redirecting to an unvalidated
		// target is an open redirect.
		client, err := s.registry.Resolve(r.Context(), clientID)
		if err != nil {
			s.log.Warn("authorize: rejecting client_id", "error", err)
			s.renderError(w, http.StatusBadRequest, "Unknown client. Register the connector again.")
			return
		}
		if redirectURI == "" || !HasRedirectURI(client, redirectURI) {
			s.log.Warn("authorize: redirect_uri is not registered", "client_id", client.ClientID)
			s.renderError(w, http.StatusBadRequest, "This redirect URI is not registered for that client.")
			return
		}

		// From here the redirect target is trustworthy, so failures go back to
		// the client as OAuth errors carrying the original state.
		state := query.Get("state")
		if code, description := s.validateAuthorizeParams(query); code != "" {
			s.redirectError(w, r, redirectURI, state, code, description)
			return
		}

		pending, err := s.newPending(&authorizeRequest{
			client:        client,
			redirectURI:   redirectURI,
			state:         state,
			codeChallenge: query.Get("code_challenge"),
			resource:      query.Get("resource"),
			scope:         s.cfg.Scope,
		})
		if err != nil {
			s.redirectError(w, r, redirectURI, state, "server_error", "could not start the authorization")
			return
		}

		s.renderLogin(w, http.StatusOK, pending, client, "")
	}
}

// validateAuthorizeParams checks everything that can be reported back to a
// trusted redirect URI. It returns an empty code when the request is valid.
func (s *AuthorizationService) validateAuthorizeParams(query url.Values) (code, description string) {
	if query.Get("response_type") != "code" {
		return "unsupported_response_type", "only the code response type is supported"
	}
	if query.Get("code_challenge") == "" {
		return "invalid_request", "code_challenge is required"
	}
	// S256 only. Accepting plain would look identical from a working client
	// and silently remove the protection PKCE exists to provide.
	if query.Get("code_challenge_method") != "S256" {
		return "invalid_request", "code_challenge_method must be S256"
	}
	if resource := query.Get("resource"); resource != "" && resource != s.cfg.Resource {
		return "invalid_target", "resource does not name this server"
	}
	if scope := query.Get("scope"); scope != "" && scope != s.cfg.Scope {
		return "invalid_scope", "the only supported scope is " + s.cfg.Scope
	}
	return "", ""
}

// newPending stores a validated authorization request awaiting login.
func (s *AuthorizationService) newPending(req *authorizeRequest) (*PendingAuthorization, error) {
	id, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	csrf, err := newOpaqueToken()
	if err != nil {
		return nil, err
	}
	now := s.cfg.now()
	pending := &PendingAuthorization{
		ID:            id,
		ClientID:      req.client.ClientID,
		RedirectURI:   req.redirectURI,
		CodeChallenge: req.codeChallenge,
		State:         req.state,
		Resource:      req.resource,
		Scope:         req.scope,
		CSRFToken:     csrf,
		CreatedAt:     now,
		ExpiresAt:     now.Add(pendingAuthorizationTTL),
	}
	if err := s.store.PutPending(pending); err != nil {
		return nil, err
	}
	return pending, nil
}

// LoginHandler serves POST /login: the password step that turns a pending
// authorization into an authorization code.
func (s *AuthorizationService) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			s.renderError(w, http.StatusMethodNotAllowed, "login requires POST")
			return
		}
		if !s.store.AllowRequest("login", clientIP(r)) {
			s.renderError(w, http.StatusTooManyRequests, "Too many login attempts. Wait a moment and try again.")
			return
		}
		// Take a slot before parsing so the whole request, not only the hash, stays
		// bounded and a rejection leaves the pending untouched.
		select {
		case s.verifySlots <- struct{}{}:
		default:
			s.renderError(w, http.StatusServiceUnavailable, "The server is busy. Try again in a moment.")
			return
		}
		defer func() { <-s.verifySlots }()
		if err := r.ParseForm(); err != nil {
			s.renderError(w, http.StatusBadRequest, "Malformed login form.")
			return
		}

		pendingID := r.PostFormValue("pending_id")
		if !s.loginAuthenticated(w, r, pendingID) {
			return
		}

		// Success consumes the pending authorization: one login, one code.
		pending, ok := s.store.TakePending(pendingID)
		if !ok {
			s.renderError(w, http.StatusBadRequest, "This login form has expired. Start the connection again.")
			return
		}
		s.issueCode(w, r, pending)
	}
}

// loginAuthenticated validates the form and the credentials, rendering the
// appropriate page and reporting false when anything fails.
func (s *AuthorizationService) loginAuthenticated(w http.ResponseWriter, r *http.Request, pendingID string) bool {
	pending, ok := s.store.GetPending(pendingID)
	if !ok {
		s.renderError(w, http.StatusBadRequest, "This login form has expired. Start the connection again.")
		return false
	}

	// The CSRF token is bound to this pending authorization, so a form
	// submitted from anywhere else fails regardless of the password.
	if subtle.ConstantTimeCompare([]byte(pending.CSRFToken), []byte(r.PostFormValue("csrf_token"))) != 1 {
		s.store.DeletePending(pendingID)
		s.renderError(w, http.StatusBadRequest, "This login form is no longer valid. Start the connection again.")
		return false
	}

	client, ok := s.store.GetClient(pending.ClientID)
	if !ok {
		s.store.DeletePending(pendingID)
		s.renderError(w, http.StatusBadRequest, "The client for this request is no longer registered.")
		return false
	}

	if s.credentialsValid(r.PostFormValue("username"), r.PostFormValue("password")) {
		return true
	}

	// Never log, echo or redisplay the password.
	remaining := s.store.RecordLoginFailure(pendingID)
	s.log.Warn("login: failed attempt", "client_ip", clientIP(r), "remaining", remaining)
	if remaining <= 0 {
		s.renderError(w, http.StatusUnauthorized, "Too many failed attempts. Start the connection again.")
		return false
	}
	s.renderLogin(w, http.StatusUnauthorized, pending, client, "Incorrect username or password.")
	return false
}

// issueCode mints an authorization code and redirects back to the client.
func (s *AuthorizationService) issueCode(w http.ResponseWriter, r *http.Request, pending *PendingAuthorization) {
	code, err := newOpaqueToken()
	if err != nil {
		s.redirectError(w, r, pending.RedirectURI, pending.State, "server_error", "could not issue a code")
		return
	}
	if err := s.store.PutCode(&AuthorizationCode{
		Code:          code,
		ClientID:      pending.ClientID,
		RedirectURI:   pending.RedirectURI,
		CodeChallenge: pending.CodeChallenge,
		Resource:      pending.Resource,
		Scope:         pending.Scope,
		ExpiresAt:     s.cfg.now().Add(s.cfg.AuthCodeTTL),
	}); err != nil {
		s.redirectError(w, r, pending.RedirectURI, pending.State, "server_error", "could not store the code")
		return
	}

	target, parseErr := url.Parse(pending.RedirectURI)
	if parseErr != nil {
		s.renderError(w, http.StatusInternalServerError, "The redirect URI could not be parsed.")
		return
	}
	query := target.Query()
	query.Set("code", code)
	if pending.State != "" {
		// State is round-tripped byte for byte: never re-encoded, never
		// trimmed. The client compares it to what it sent.
		query.Set("state", pending.State)
	}
	// RFC 9207: naming the issuer in the response lets the client detect a
	// code injected from a different authorization server.
	query.Set("iss", s.cfg.Issuer)
	target.RawQuery = query.Encode()

	s.log.Info("login: issued an authorization code", "client_id", pending.ClientID)
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// credentialsValid compares the submitted credentials against the configured
// username and stored password hash.
func (s *AuthorizationService) credentialsValid(username, password string) bool {
	// Both comparisons run every time and in constant time, so a wrong
	// username and a wrong password cost the same.
	userOK := subtle.ConstantTimeCompare([]byte(s.cfg.LoginUser), []byte(username)) == 1
	passOK := s.verifyPassword(s.cfg.LoginPasswordHash, password)
	return userOK && passOK
}

// redirectError returns an OAuth error to the client's redirect URI.
func (s *AuthorizationService) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "The redirect URI could not be parsed.")
		return
	}
	query := target.Query()
	query.Set("error", code)
	query.Set("error_description", description)
	if state != "" {
		query.Set("state", state)
	}
	query.Set("iss", s.cfg.Issuer)
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// loginPageData is the template model for the login form.
type loginPageData struct {
	PendingID  string
	CSRFToken  string
	ClientName string
	Resource   string
	LoginPath  string
	Error      string
}

// renderLogin renders the combined login and consent page.
//
// There is one credential and one user, so a separate consent screen after a
// password prompt would ask the same person the same question twice. The form
// names the client and the resource above the password field instead.
func (s *AuthorizationService) renderLogin(w http.ResponseWriter, status int, pending *PendingAuthorization, client *Client, message string) {
	name := client.ClientName
	if name == "" {
		name = client.ClientID
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	if err := loginTemplate.Execute(w, loginPageData{
		PendingID:  pending.ID,
		CSRFToken:  pending.CSRFToken,
		ClientName: name,
		Resource:   s.cfg.Resource,
		LoginPath:  "login",
		Error:      message,
	}); err != nil {
		s.log.Error("rendering the login page", "error", err)
	}
}

// renderError renders a terminal error page, used wherever redirecting would
// mean trusting an unvalidated URI.
func (s *AuthorizationService) renderError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := errorTemplate.Execute(w, map[string]string{"Message": message}); err != nil {
		s.log.Error("rendering the error page", "error", err)
	}
}

// loginTemplate is html/template rather than text/template deliberately: the
// client name and resource rendered here are attacker-supplied, and
// text/template would inject them raw.
var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TimeMCP — sign in</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, -apple-system, sans-serif; margin: 0;
       display: grid; place-items: center; min-height: 100vh; padding: 1rem; }
main { width: 100%; max-width: 26rem; }
h1 { font-size: 1.25rem; margin: 0 0 0.25rem; }
p.sub { margin: 0 0 1.5rem; opacity: 0.7; font-size: 0.9rem; }
label { display: block; margin-bottom: 1rem; font-size: 0.85rem; }
input { width: 100%; box-sizing: border-box; padding: 0.6rem;
        font-size: 1rem; margin-top: 0.25rem; border-radius: 6px;
        border: 1px solid rgba(128,128,128,0.5); background: transparent;
        color: inherit; }
button { width: 100%; padding: 0.7rem; font-size: 1rem; border: 0;
         border-radius: 6px; cursor: pointer; }
.err { padding: 0.6rem 0.8rem; border-radius: 6px; margin-bottom: 1rem;
       background: rgba(220,50,50,0.12); font-size: 0.9rem; }
.grant { font-size: 0.85rem; opacity: 0.8; margin-bottom: 1.25rem;
         padding: 0.6rem 0.8rem; border-radius: 6px;
         background: rgba(128,128,128,0.12); }
code { word-break: break-all; }
</style>
</head>
<body>
<main>
<h1>TimeMCP</h1>
<p class="sub">time and timezone utilities</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
<div class="grant">
<strong>{{.ClientName}}</strong> is asking to search the web as you on
<code>{{.Resource}}</code>. Signing in grants it that access.
</div>
<form method="post" action="{{.LoginPath}}">
<input type="hidden" name="pending_id" value="{{.PendingID}}">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>Username
<input type="text" name="username" autocomplete="username" autofocus required>
</label>
<label>Password
<input type="password" name="password" autocomplete="current-password" required>
</label>
<button type="submit">Sign in</button>
</form>
</main>
</body>
</html>
`))

// errorTemplate renders a terminal error with no link back, since the only
// safe recovery is for the client to restart the flow.
var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TimeMCP — error</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, -apple-system, sans-serif; margin: 0;
       display: grid; place-items: center; min-height: 100vh; padding: 1rem; }
main { max-width: 26rem; }
h1 { font-size: 1.1rem; margin: 0 0 0.5rem; }
p { margin: 0; opacity: 0.8; font-size: 0.95rem; }
</style>
</head>
<body>
<main>
<h1>This request could not be authorized</h1>
<p>{{.Message}}</p>
</main>
</body>
</html>
`))
