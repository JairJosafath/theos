package awsenv

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// price is the on-demand hourly price of the environment's instance type in
// its region, for Linux on shared tenancy, in millionths of a US dollar, as
// the AWS Price List says: read once per type and region. Where it lists
// more than one, the highest counts, so a budget never counts less than EC2
// bills.
func (p *Provider) price(ctx context.Context, s *session) (int64, error) {
	key := s.env.AWS.Region + "/" + s.env.AWS.InstanceType
	p.mu.Lock()
	price, ok := p.prices[key]
	p.mu.Unlock()
	if ok {
		return price, nil
	}
	match := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String(field), Value: aws.String(value)}
	}
	in := &pricing.GetProductsInput{ServiceCode: aws.String("AmazonEC2"), FormatVersion: aws.String("aws_v1"),
		Filters: []pricingtypes.Filter{
			match("instanceType", s.env.AWS.InstanceType), match("regionCode", s.env.AWS.Region),
			match("operatingSystem", "Linux"), match("tenancy", "Shared"), match("preInstalledSw", "NA"),
			match("capacitystatus", "Used"), match("licenseModel", "No License required"),
		}}
	for {
		out, err := s.clients.Pricing.GetProducts(ctx, in)
		if err != nil {
			return 0, portError(fmt.Errorf("reading the price of %s in %s: %w", s.env.AWS.InstanceType,
				s.env.AWS.Region, err))
		}
		for _, doc := range out.PriceList {
			hourly, err := onDemandHourly(doc)
			if err != nil {
				return 0, err
			}
			price = max(price, hourly)
		}
		if aws.ToString(out.NextToken) == "" {
			break
		}
		in.NextToken = out.NextToken
	}
	if price == 0 {
		return 0, fmt.Errorf("the AWS Price List has no on-demand Linux price for instance type %s in region %s",
			s.env.AWS.InstanceType, s.env.AWS.Region)
	}
	p.mu.Lock()
	if p.prices == nil {
		p.prices = map[string]int64{}
	}
	p.prices[key] = price
	p.mu.Unlock()
	return price, nil
}

// priceDocument is the part of a Price List product the price is read from.
type priceDocument struct {
	Terms struct {
		OnDemand map[string]struct {
			PriceDimensions map[string]struct {
				Unit         string            `json:"unit"`
				PricePerUnit map[string]string `json:"pricePerUnit"`
			} `json:"priceDimensions"`
		} `json:"OnDemand"`
	} `json:"terms"`
}

// onDemandHourly is the highest on-demand price an hour in US dollars that a
// Price List product names, in millionths; 0 when it names none.
func onDemandHourly(doc string) (int64, error) {
	var d priceDocument
	if err := json.Unmarshal([]byte(doc), &d); err != nil {
		return 0, fmt.Errorf("reading a Price List product: %w", err)
	}
	var out int64
	for _, term := range d.Terms.OnDemand {
		for _, dim := range term.PriceDimensions {
			if !strings.EqualFold(dim.Unit, "Hrs") {
				continue
			}
			usd, ok := dim.PricePerUnit["USD"]
			if !ok {
				continue
			}
			micro, err := microUSD(usd)
			if err != nil {
				return 0, err
			}
			out = max(out, micro)
		}
	}
	return out, nil
}

// microUSD is a decimal number of US dollars, such as 1.0060000000, in
// millionths, rounded up.
func microUSD(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	bad := func() (int64, error) { return 0, fmt.Errorf("the Price List names the price %q", s) }
	if whole == "" || strings.Trim(whole, "0123456789") != "" || strings.Trim(frac, "0123456789") != "" ||
		len(whole) > 9 {
		return bad()
	}
	dollars, _ := strconv.ParseInt(whole, 10, 64)
	digits := frac + "000000"
	micro, _ := strconv.ParseInt(digits[:6], 10, 64)
	if strings.Trim(frac[min(len(frac), 6):], "0") != "" {
		micro++
	}
	return dollars*1_000_000 + micro, nil
}
