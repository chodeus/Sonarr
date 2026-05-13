package twitter

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// oauthSigner generates the OAuth1 Authorization header for a request
// against the Twitter API. Mirrors the C# `OAuthRequest.GetAuthorizationHeader`
// surface used by `TwitterProxy` — the Go port re-implements the
// minimal HMAC-SHA1 signing path inline since the .NET helper package
// isn't a portable target.
//
// Algorithm (RFC 5849):
//  1. Collect oauth_* params + body params.
//  2. Percent-encode keys + values, sort lexicographically.
//  3. Base string = METHOD&percent(URL)&percent(joined-params).
//  4. Signing key = percent(consumer_secret)&percent(token_secret).
//  5. Signature = base64(HMAC-SHA1(base, key)).
//  6. Authorization header = `OAuth k1="v1", k2="v2", ...` of just the
//     oauth_* params + the signature.
type oauthSigner struct {
	consumerKey    string
	consumerSecret string
	tokenKey       string
	tokenSecret    string

	// nowFn / nonceFn are test seams — defaults to time.Now and a
	// crypto/rand-backed 32-byte hex string respectively.
	nowFn   func() time.Time
	nonceFn func() string
}

// newSigner constructs an oauthSigner with the production clock +
// nonce source.
func newSigner(consumerKey, consumerSecret, tokenKey, tokenSecret string) *oauthSigner {
	return &oauthSigner{
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		tokenKey:       tokenKey,
		tokenSecret:    tokenSecret,
		nowFn:          time.Now,
		nonceFn:        defaultNonce,
	}
}

// defaultNonce returns a 32-hex-char random nonce — matches the
// length the Twitter docs recommend.
func defaultNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// AuthorizationHeader signs a request against the target endpoint
// with the supplied body params + returns the `OAuth ...` header
// value.
//
// method is upper-case ("GET" / "POST"). endpoint is the full URL
// MINUS query string. bodyParams is the application/x-www-form-
// urlencoded payload key/value set; both keys and values must NOT
// be pre-percent-encoded — the signer encodes them once per RFC 5849.
func (o *oauthSigner) AuthorizationHeader(method, endpoint string, bodyParams map[string]string) string {
	return o.authorizationHeaderWithExtras(method, endpoint, bodyParams, nil)
}

// authorizationHeaderWithExtras allows the OAuth1 request-token /
// access-token flows to inject `oauth_callback` / `oauth_verifier`
// into the protocol params. extras keys must start with `oauth_` to
// be useful; they MUST be excluded from the body params.
func (o *oauthSigner) authorizationHeaderWithExtras(method, endpoint string, bodyParams, extras map[string]string) string {
	oauthParams := map[string]string{
		"oauth_consumer_key":     o.consumerKey,
		"oauth_nonce":            o.nonceFn(),
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(o.nowFn().Unix(), 10),
		"oauth_version":          "1.0",
	}
	// oauth_token is OMITTED in the request_token flow (per RFC 5849).
	// Only include it when the signer carries a non-empty token key.
	if o.tokenKey != "" {
		oauthParams["oauth_token"] = o.tokenKey
	}
	for k, v := range extras {
		oauthParams[k] = v
	}
	signature := o.sign(method, endpoint, oauthParams, bodyParams)
	oauthParams["oauth_signature"] = signature

	// Header carries the oauth_* params only, with values percent-
	// encoded + quoted.
	keys := make([]string, 0, len(oauthParams))
	for k := range oauthParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+`="`+percentEncode(oauthParams[k])+`"`)
	}
	return "OAuth " + strings.Join(parts, ", ")
}

// sign computes the base64-HMAC-SHA1 signature per RFC 5849.
func (o *oauthSigner) sign(method, endpoint string, oauthParams, bodyParams map[string]string) string {
	// Merge oauth + body params for the signature base string.
	all := make(map[string]string, len(oauthParams)+len(bodyParams))
	for k, v := range oauthParams {
		all[k] = v
	}
	for k, v := range bodyParams {
		all[k] = v
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	encPairs := make([]string, 0, len(keys))
	for _, k := range keys {
		encPairs = append(encPairs, percentEncode(k)+"="+percentEncode(all[k]))
	}
	paramString := strings.Join(encPairs, "&")
	base := strings.ToUpper(method) + "&" + percentEncode(endpoint) + "&" + percentEncode(paramString)
	key := percentEncode(o.consumerSecret) + "&" + percentEncode(o.tokenSecret)
	mac := hmac.New(sha1.New, []byte(key))
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// percentEncode mirrors the C# `EncodeRFC3986` extension — encodes
// reserved chars but keeps the unreserved set (alphanum + - _ . ~).
// The default [url.QueryEscape] uses `+` for spaces; OAuth1 wants `%20`.
func percentEncode(s string) string {
	var out strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			out.WriteByte(c)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", c)
	}
	return out.String()
}
