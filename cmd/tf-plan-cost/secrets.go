package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ssmPrefix marks a configuration value stored in AWS SSM Parameter Store,
// e.g. GITHUB_PRIVATE_KEY=ssm:/tf-plan-cost/private-key.
const ssmPrefix = "ssm:"

// secretVars may be given as SSM parameter references.
var secretVars = []string{"GITHUB_APP_ID", "GITHUB_PRIVATE_KEY", "GITHUB_WEBHOOK_SECRET"}

// ssmAPI is the subset of the SSM client used here.
type ssmAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// newSSMClient builds the SSM client; tests replace it.
var newSSMClient = func(ctx context.Context) (ssmAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return ssm.NewFromConfig(cfg), nil
}

// resolveSecrets returns a getenv that reads secretVars set to "ssm:<name>"
// from Parameter Store (decrypted), once, at start-up. Other variables and
// plain values pass through, so local runs need no AWS access.
func resolveSecrets(ctx context.Context, getenv func(string) string) (func(string) string, error) {
	resolved := map[string]string{}
	var client ssmAPI
	for _, name := range secretVars {
		ref, ok := strings.CutPrefix(getenv(name), ssmPrefix)
		if !ok {
			continue
		}
		if ref == "" {
			return nil, fmt.Errorf("%s: empty SSM parameter name", name)
		}
		if client == nil {
			var err error
			if client, err = newSSMClient(ctx); err != nil {
				return nil, err
			}
		}
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: &ref, WithDecryption: new(true)})
		if err != nil {
			return nil, fmt.Errorf("%s: read SSM parameter %s: %w", name, ref, err)
		}
		if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
			return nil, fmt.Errorf("%s: SSM parameter %s is empty", name, ref)
		}
		resolved[name] = strings.TrimSpace(*out.Parameter.Value)
	}
	if len(resolved) == 0 {
		return getenv, nil
	}
	return func(k string) string {
		if v, ok := resolved[k]; ok {
			return v
		}
		return getenv(k)
	}, nil
}
