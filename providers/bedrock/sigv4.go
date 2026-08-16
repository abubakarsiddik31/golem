package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials are static AWS credentials. Applications obtain them from
// their environment or identity provider and wire them in explicitly —
// the adapter never reads the environment.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken accompanies temporary credentials; empty for
	// long-lived keys.
	SessionToken string
}

// signV4 adds AWS Signature Version 4 authentication headers to req for
// the given service and region. body is the exact request payload, whose
// hash is signed; now stamps the request. Reproducible against AWS's
// documented signing example, pinned in sigv4_test.go.
func signV4(req *http.Request, body []byte, credentials Credentials, region, service string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")

	req.Header.Set("x-amz-date", amzDate)
	if credentials.SessionToken != "" {
		req.Header.Set("x-amz-security-token", credentials.SessionToken)
	}

	canonicalHeaders, signedHeaders := canonicalHeaderBlock(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		sha256Hex(body),
	}, "\n")

	scope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+credentials.SecretAccessKey), dateStamp)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		credentials.AccessKeyID, scope, signedHeaders, signature))
}

// canonicalHeaderBlock renders the canonical headers block (each
// "name:value" pair followed by a newline) and the semicolon-joined
// signed header names, both over the headers that participate in
// signing: content-type when set, host, x-amz-date, and the session
// token when present.
func canonicalHeaderBlock(req *http.Request) (block, names string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	pairs := map[string]string{
		"host":       host,
		"x-amz-date": req.Header.Get("x-amz-date"),
	}
	if contentType := req.Header.Get("Content-Type"); contentType != "" {
		pairs["content-type"] = contentType
	}
	if token := req.Header.Get("x-amz-security-token"); token != "" {
		pairs["x-amz-security-token"] = token
	}

	sorted := make([]string, 0, len(pairs))
	for name := range pairs {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var builder strings.Builder
	for _, name := range sorted {
		builder.WriteString(name)
		builder.WriteString(":")
		builder.WriteString(strings.TrimSpace(pairs[name]))
		builder.WriteString("\n")
	}
	return builder.String(), strings.Join(sorted, ";")
}

// canonicalQuery renders the canonical query string: parameters sorted
// by name with URL-encoded names and values.
func canonicalQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		// Preserve the raw query when it cannot be reparsed; the signed
		// request uses it verbatim either way.
		return u.RawQuery
	}
	return values.Encode()
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
