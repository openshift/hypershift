package metrics

type PriceItemInstance struct {
	Product ProductInfoInstance `json:"product"`
}

type ProductInfoInstance struct {
	Attributes ProductAttributesInstance `json:"attributes"`
}

type ProductAttributesInstance struct {
	VCPU string `json:"vcpu"`
}
