package bedrock

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSignV4MatchesDocumentedAWSExample pins the signer to the worked
// example in AWS's Signature Version 4 documentation: a ListUsers request
// to IAM whose canonical-request hash and final signature are published.
// If this test fails, the signing chain deviates from AWS.
func TestSignV4MatchesDocumentedAWSExample(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet,
		"https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	signV4(request, nil, Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}, "us-east-1", "iam", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))

	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n  %s\nwant\n  %s", got, want)
	}
	if got := request.Header.Get("x-amz-date"); got != "20150830T123600Z" {
		t.Fatalf("x-amz-date = %q", got)
	}
}

// TestSignV4IncludesSessionToken signs temporary credentials: the token
// rides the request and joins the signed headers, in deterministic order.
func TestSignV4IncludesSessionToken(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost,
		"https://bedrock-runtime.us-east-1.amazonaws.com/model/m/converse", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")

	signV4(request, []byte("{}"), Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "secret",
		SessionToken:    "session-token",
	}, "us-east-1", "bedrock", time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))

	if got := request.Header.Get("x-amz-security-token"); got != "session-token" {
		t.Fatalf("x-amz-security-token = %q", got)
	}
	authorization := request.Header.Get("Authorization")
	if !strings.Contains(authorization, "SignedHeaders=content-type;host;x-amz-date;x-amz-security-token,") {
		t.Fatalf("Authorization = %q, want the session token in the signed headers", authorization)
	}
	if !strings.Contains(authorization, "/20260816/us-east-1/bedrock/aws4_request") {
		t.Fatalf("Authorization = %q, want the bedrock credential scope", authorization)
	}
}
