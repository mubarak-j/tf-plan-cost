package pricing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awspricing "github.com/aws/aws-sdk-go-v2/service/pricing"
)

type fakeAPI struct {
	pages  []*awspricing.GetProductsOutput
	inputs []*awspricing.GetProductsInput
	err    error
}

func (f *fakeAPI) GetProducts(_ context.Context, in *awspricing.GetProductsInput, _ ...func(*awspricing.Options)) (*awspricing.GetProductsOutput, error) {
	cp := *in
	f.inputs = append(f.inputs, &cp)
	if f.err != nil {
		return nil, f.err
	}
	return f.pages[len(f.inputs)-1], nil
}

func TestAWSClientLookup(t *testing.T) {
	api := &fakeAPI{pages: []*awspricing.GetProductsOutput{
		{PriceList: []string{product("A", "UnusedBox:db.t4g.micro", dim("A.1", "Hrs", "0.016", "0"))}, NextToken: aws.String("page2")},
		{PriceList: []string{product("B", "InstanceUsage:db.t4g.micro", dim("B.1", "Hrs", "0.016", "0"))}},
	}}
	c := NewAWSClientFromAPI(api)
	fixed := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return fixed }

	q := Query{
		ServiceCode: "AmazonRDS",
		Filters:     map[string]string{"regionCode": "us-east-1", "instanceType": "db.t4g.micro"},
		UsageType:   "InstanceUsage:db.t4g.micro",
	}
	p, err := c.Lookup(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if p.SKU != "B" || p.USD.String() != "0.016" || !p.FetchedAt.Equal(fixed) {
		t.Errorf("got %+v", p)
	}
	if len(api.inputs) != 2 || aws.ToString(api.inputs[1].NextToken) != "page2" {
		t.Fatalf("pagination: %d calls", len(api.inputs))
	}
	in := api.inputs[0]
	if aws.ToString(in.ServiceCode) != "AmazonRDS" || len(in.Filters) != 2 {
		t.Fatalf("input = %+v", in)
	}
	// Filters are sent in sorted order.
	if aws.ToString(in.Filters[0].Field) != "instanceType" || aws.ToString(in.Filters[1].Value) != "us-east-1" {
		t.Errorf("filters = %v %v", aws.ToString(in.Filters[0].Field), aws.ToString(in.Filters[1].Field))
	}
}

func TestAWSClientErrors(t *testing.T) {
	apiErr := errors.New("no credentials")
	_, err := NewAWSClientFromAPI(&fakeAPI{err: apiErr}).Lookup(context.Background(), Query{ServiceCode: "AmazonEC2"})
	if !errors.Is(err, apiErr) {
		t.Errorf("err = %v, want wrapped API error", err)
	}
	if _, ok := UnpricedReason(err); ok {
		t.Error("API failures must not be treated as unpriced")
	}

	// Endless pagination stops.
	var pages []*awspricing.GetProductsOutput
	for range maxPages + 1 {
		pages = append(pages, &awspricing.GetProductsOutput{NextToken: aws.String("more")})
	}
	_, err = NewAWSClientFromAPI(&fakeAPI{pages: pages}).Lookup(context.Background(), Query{ServiceCode: "AmazonEC2"})
	if !errors.Is(err, ErrAmbiguousPrice) {
		t.Errorf("err = %v, want ErrAmbiguousPrice", err)
	}
}
