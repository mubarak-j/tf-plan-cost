package pricing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awspricing "github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// PriceListRegion is where the Price List Query API is served from. Prices
// for every region are available from this endpoint.
const PriceListRegion = "us-east-1"

// maxPages bounds how many result pages one query may read. Queries are
// expected to match one product; this guards against overly broad filters.
const maxPages = 5

// GetProductsAPI is the subset of the AWS Pricing client used here.
type GetProductsAPI interface {
	GetProducts(ctx context.Context, in *awspricing.GetProductsInput, opts ...func(*awspricing.Options)) (*awspricing.GetProductsOutput, error)
}

// AWSClient looks up prices with the AWS Price List Query API
// (pricing:GetProducts). It requires AWS credentials.
type AWSClient struct {
	api GetProductsAPI
	now func() time.Time
}

// NewAWSClient builds a client from the default AWS credential chain
// (environment, shared config/SSO profile, IMDS, OIDC web identity, ...).
func NewAWSClient(ctx context.Context) (*AWSClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(PriceListRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return NewAWSClientFromAPI(awspricing.NewFromConfig(cfg)), nil
}

// NewAWSClientFromAPI wraps an existing GetProducts implementation.
func NewAWSClientFromAPI(api GetProductsAPI) *AWSClient {
	return &AWSClient{api: api, now: time.Now}
}

// Lookup implements PriceClient.
func (c *AWSClient) Lookup(ctx context.Context, q Query) (UnitPrice, error) {
	keys := make([]string, 0, len(q.Filters))
	for k := range q.Filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	filters := make([]types.Filter, 0, len(keys))
	for _, k := range keys {
		filters = append(filters, types.Filter{
			Type:  types.FilterTypeTermMatch,
			Field: aws.String(k),
			Value: aws.String(q.Filters[k]),
		})
	}

	in := &awspricing.GetProductsInput{
		ServiceCode:   aws.String(q.ServiceCode),
		Filters:       filters,
		FormatVersion: aws.String("aws_v1"),
		MaxResults:    aws.Int32(100),
	}
	var products []string
	for page := 0; ; page++ {
		if page == maxPages {
			return UnitPrice{}, fmt.Errorf("%w: query matched more than %d pages: %s", ErrAmbiguousPrice, maxPages, q)
		}
		out, err := c.api.GetProducts(ctx, in)
		if err != nil {
			return UnitPrice{}, fmt.Errorf("pricing:GetProducts %s: %w", q, err)
		}
		products = append(products, out.PriceList...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		in.NextToken = out.NextToken
	}

	price, err := selectPrice(products, q)
	if err != nil {
		return UnitPrice{}, err
	}
	price.FetchedAt = c.now().UTC()
	return price, nil
}
