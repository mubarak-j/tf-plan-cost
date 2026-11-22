package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	params map[string]string
	calls  []string
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls = append(f.calls, *in.Name)
	if in.WithDecryption == nil || !*in.WithDecryption {
		return nil, errors.New("not decrypted")
	}
	v, ok := f.params[*in.Name]
	if !ok {
		return nil, errors.New("ParameterNotFound")
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: &v}}, nil
}

func withFakeSSM(t *testing.T, f *fakeSSM) {
	t.Helper()
	old := newSSMClient
	newSSMClient = func(context.Context) (ssmAPI, error) { return f, nil }
	t.Cleanup(func() { newSSMClient = old })
}

func TestResolveSecrets(t *testing.T) {
	f := &fakeSSM{params: map[string]string{"/tpc/key": "-----BEGIN KEY-----\n", "/tpc/secret": "s3cret\n"}}
	withFakeSSM(t, f)

	getenv, err := resolveSecrets(context.Background(), envFunc(map[string]string{
		"GITHUB_APP_ID": "123", "GITHUB_PRIVATE_KEY": "ssm:/tpc/key", "GITHUB_WEBHOOK_SECRET": "ssm:/tpc/secret",
		"PORT": "ssm:/not/a/secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := getenv("GITHUB_PRIVATE_KEY"); got != "-----BEGIN KEY-----" {
		t.Errorf("private key = %q", got)
	}
	if got := getenv("GITHUB_WEBHOOK_SECRET"); got != "s3cret" {
		t.Errorf("secret = %q (should be trimmed)", got)
	}
	if getenv("GITHUB_APP_ID") != "123" || getenv("PORT") != "ssm:/not/a/secret" {
		t.Errorf("non-SSM values changed")
	}
	if len(f.calls) != 2 {
		t.Errorf("SSM calls = %v", f.calls)
	}
}

func TestResolveSecretsNoSSM(t *testing.T) {
	withFakeSSM(t, nil) // must not be called
	newSSMClient = func(context.Context) (ssmAPI, error) {
		t.Error("SSM client created without ssm: values")
		return nil, errors.New("unused")
	}
	if _, err := resolveSecrets(context.Background(), envFunc(map[string]string{"GITHUB_APP_ID": "1"})); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSecretsErrors(t *testing.T) {
	withFakeSSM(t, &fakeSSM{params: map[string]string{"/empty": ""}})
	for name, value := range map[string]string{"missing": "ssm:/nope", "empty name": "ssm:", "empty value": "ssm:/empty"} {
		_, err := resolveSecrets(context.Background(), envFunc(map[string]string{"GITHUB_WEBHOOK_SECRET": value}))
		if err == nil || !strings.Contains(err.Error(), "GITHUB_WEBHOOK_SECRET") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
