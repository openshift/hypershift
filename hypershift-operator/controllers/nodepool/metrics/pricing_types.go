package metrics

type PricingDataInstance struct {
	FormatVersion string              `json:"FormatVersion"`
	NextToken     string              `json:"NextToken"`
	PriceList     []PriceItemInstance `json:"PriceList"`
}
type PriceItemInstance struct {
	Product         ProductInfoInstance `json:"product"`
	PublicationDate string              `json:"publicationDate"`
	ServiceCode     string              `json:"serviceCode"`
	Terms           TermsInstance       `json:"terms"`
}
type ProductInfoInstance struct {
	Attributes    ProductAttributesInstance `json:"attributes"`
	ProductFamily string                    `json:"productFamily"`
}
type ProductAttributesInstance struct {
	InstanceFamily  string `json:"instanceFamily"`
	InstanceType    string `json:"instanceType"`
	Memory          string `json:"memory"`
	RegionCode      string `json:"regionCode"`
	ServiceCode     string `json:"servicecode"`
	ServiceName     string `json:"servicename"`
	Tenancy         string `json:"tenancy"`
	UsageType       string `json:"usagetype"`
	VCPU            string `json:"vcpu"`
	OperatingSystem string `json:"operatingSystem"`
	ClockSpeed      string `json:"clockSpeed"`
}
type TermsInstance struct {
	OnDemand map[string]OnDemandTermInstance `json:"OnDemand"`
}
type OnDemandTermInstance struct {
	EffectiveDate   string                            `json:"effectiveDate"`
	PriceDimensions map[string]PriceDimensionInstance `json:"priceDimensions"`
}
type PriceDimensionInstance struct {
	PricePerUnit PricePerUnitInstance `json:"pricePerUnit"`
	Unit         string               `json:"unit"`
	Description  string               `json:"description"`
}
type PricePerUnitInstance struct {
	USD string `json:"USD"`
}