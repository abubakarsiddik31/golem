package mcp

import "strings"

// Challenge is the Bearer challenge a 401 or 403 response carries in its
// WWW-Authenticate header, as RFC 6750 defines it. A field the server did
// not send is empty.
type Challenge struct {
	// Scheme is the challenge's authentication scheme, spelled as the
	// server spelled it; compare it case-insensitively. A challenge
	// this package returns has matched "Bearer".
	Scheme string
	// Realm names the protection space the credentials apply to.
	Realm string
	// Error is the RFC 6750 error code: invalid_request,
	// invalid_token, or insufficient_scope.
	Error string
	// Description is the server's human-readable explanation.
	Description string
	// Scope is the space-delimited set of scopes the operation needs.
	// It is normally present on an insufficient_scope challenge.
	Scope string
	// ResourceMetadata is the protected-resource metadata URL an
	// application discovers the authorization server from (RFC 9728).
	// It is normally present on a 401.
	ResourceMetadata string
}

// AuthError reports a 401 or 403 response: the server refused the
// request's credentials. The HTTP transport returns it instead of a bare
// *HTTPStatusError, and it unwraps to one, so code that only inspects the
// status keeps working.
//
// The transport never retries and never obtains a token. It reports what
// the server asked for; re-authorizing and retrying is the application's
// decision.
type AuthError struct {
	// Status is the underlying HTTP status error.
	Status *HTTPStatusError
	// Header is the response's raw WWW-Authenticate value, empty when
	// the server sent none.
	Header string
	// Challenge is the parsed Bearer challenge. All its fields are
	// empty when the response carried no Bearer challenge.
	Challenge Challenge
}

func (e *AuthError) Error() string {
	message := "mcp: request was not authorized"
	if e.Status != nil {
		message = e.Status.Error()
	}
	if e.Challenge.Error != "" {
		return message + ": " + e.Challenge.Error
	}
	return message
}

// Unwrap returns the underlying HTTP status error.
func (e *AuthError) Unwrap() error {
	return e.Status
}

// parseBearerChallenge returns the Bearer challenge in a
// WWW-Authenticate value, or the zero Challenge when there is none. A
// header may carry one challenge per scheme and a challenge's parameters
// are also comma-separated, so the value is scanned rather than split on
// commas. Token68 credentials, which MCP servers do not use, are not
// interpreted.
func parseBearerChallenge(header string) Challenge {
	for _, challenge := range parseChallenges(header) {
		if strings.EqualFold(challenge.Scheme, "Bearer") {
			return challenge
		}
	}
	return Challenge{}
}

// parseChallenges splits a WWW-Authenticate value into its challenges.
func parseChallenges(value string) []Challenge {
	var challenges []Challenge
	scanner := &authScanner{value: value}
	for {
		scanner.space()
		for scanner.peek() == ',' {
			scanner.at++
			scanner.space()
		}
		if scanner.done() {
			return challenges
		}
		scheme := scanner.token()
		if scheme == "" {
			// Not a challenge boundary; step over the byte.
			scanner.at++
			continue
		}
		challenges = append(challenges, scanner.parameters(scheme))
	}
}

// authScanner walks a WWW-Authenticate value byte by byte.
type authScanner struct {
	value string
	at    int
}

func (s *authScanner) done() bool {
	return s.at >= len(s.value)
}

func (s *authScanner) peek() byte {
	if s.done() {
		return 0
	}
	return s.value[s.at]
}

// space skips optional whitespace.
func (s *authScanner) space() {
	for !s.done() && (s.value[s.at] == ' ' || s.value[s.at] == '\t') {
		s.at++
	}
}

// token reads an RFC 7230 token.
func (s *authScanner) token() string {
	start := s.at
	for !s.done() && tokenByte(s.value[s.at]) {
		s.at++
	}
	return s.value[start:s.at]
}

// quoted reads a quoted-string and returns its unescaped content. A
// string that never closes consumes the rest of the value.
func (s *authScanner) quoted() string {
	s.at++ // opening quote
	var out strings.Builder
	for !s.done() {
		switch c := s.value[s.at]; c {
		case '\\':
			s.at++
			if !s.done() {
				out.WriteByte(s.value[s.at])
				s.at++
			}
		case '"':
			s.at++
			return out.String()
		default:
			out.WriteByte(c)
			s.at++
		}
	}
	return out.String()
}

// parameters reads one challenge's auth-params, stopping where the next
// challenge's scheme begins.
func (s *authScanner) parameters(scheme string) Challenge {
	challenge := Challenge{Scheme: scheme}
	params := map[string]string{}
	s.space()
	for {
		if s.done() {
			break
		}
		if s.peek() == ',' {
			s.at++
			s.space()
			if s.done() {
				break
			}
		}
		start := s.at
		name := s.token()
		if name == "" {
			// A byte that cannot start a parameter; leave it to the
			// outer scan.
			s.at = start
			break
		}
		if s.peek() != '=' {
			// A bare token starts the next challenge's scheme.
			s.at = start
			break
		}
		s.at++ // '='
		var value string
		if s.peek() == '"' {
			value = s.quoted()
		} else {
			value = s.token()
		}
		params[strings.ToLower(name)] = value
		s.space()
	}
	challenge.Realm = params["realm"]
	challenge.Error = params["error"]
	challenge.Description = params["error_description"]
	challenge.Scope = params["scope"]
	challenge.ResourceMetadata = params["resource_metadata"]
	return challenge
}

// tokenByte reports whether c may appear in an RFC 7230 token.
func tokenByte(c byte) bool {
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// bearerToken reports whether a token can be sent as a bearer credential:
// visible ASCII with no surrounding whitespace, so it cannot split the
// header or inject another one.
func bearerToken(token string) bool {
	if token == "" || token != strings.TrimSpace(token) {
		return false
	}
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return false
		}
	}
	return true
}
