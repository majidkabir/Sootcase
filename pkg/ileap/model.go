// Package ileap writes Sootcase results in the data model of the iLEAP
// Technical Specifications (v1.1.0, SINE Foundation and Smart Freight
// Centre, https://specs.ileap.global/): ShipmentFootprints made of
// Transport Chain Elements (TCEs), and the Transport and Hub Operation
// Categories (TOCs, HOCs) they were calculated with.
//
// iLEAP makes some properties mandatory that a calculation on default
// values can't fill: an energy carrier that a default value doesn't name
// (e.g. GLEC's rail "traction unknown"), or a TTW value for GLEC's hub
// defaults, which are given as WTW only. Sootcase never invents those.
// It leaves the property out and lists it as a Gap; an Export with gaps
// is not conformant, and says so.
package ileap

// SpecVersion is the version of the iLEAP Technical Specifications the
// objects follow.
const SpecVersion = "1.1.0"

// SpecURL is where that version is published.
const SpecURL = "https://specs.ileap.global/"

// Decimal is a decimal number encoded as a JSON string, as iLEAP asks.
type Decimal string

// ShipmentFootprint is the footprint of one shipment (iLEAP 6.1).
type ShipmentFootprint struct {
	SpecVersion                    string      `json:"specVersion"`
	CompanyName                    string      `json:"companyName,omitempty"`
	CompanyIDs                     []string    `json:"companyIds,omitempty"`
	Description                    string      `json:"description,omitempty"`
	CreatedAt                      string      `json:"createdAt"`
	Status                         string      `json:"status"`
	ReferencePeriodStart           string      `json:"referencePeriodStart,omitempty"`
	ReferencePeriodEnd             string      `json:"referencePeriodEnd,omitempty"`
	SecondaryEmissionFactorSources []SourceRef `json:"secondaryEmissionFactorSources,omitempty"`
	Comment                        string      `json:"comment,omitempty"`
	Mass                           Decimal     `json:"mass"`
	ShipmentID                     string      `json:"shipmentId"`
	TCEs                           []TCE       `json:"tces"`
}

// TCE is one transport or hub element of a shipment's transport chain
// (iLEAP 6.2).
type TCE struct {
	TCEID             string       `json:"tceId"`
	PrevTCEIDs        []string     `json:"prevTceIds,omitempty"`
	TOCID             string       `json:"tocId,omitempty"`
	HOCID             string       `json:"hocId,omitempty"`
	ShipmentID        string       `json:"shipmentId"`
	Mass              Decimal      `json:"mass"`
	PackagingOrTrEq   string       `json:"packagingOrTrEqType,omitempty"`
	PackagingAmount   Decimal      `json:"packagingOrTrEqAmount,omitempty"`
	Distance          GLECDistance `json:"distance"`
	Origin            *Location    `json:"origin,omitempty"`
	Destination       *Location    `json:"destination,omitempty"`
	TransportActivity Decimal      `json:"transportActivity"`
	CO2eWTW           Decimal      `json:"co2eWTW"`
	CO2eTTW           Decimal      `json:"co2eTTW,omitempty"` // mandatory; empty only when unknown (a Gap)
}

// TOC is a transport operation category (iLEAP 6.3).
type TOC struct {
	SpecVersion                    string          `json:"specVersion"`
	CompanyName                    string          `json:"companyName,omitempty"`
	CompanyIDs                     []string        `json:"companyIds,omitempty"`
	CreatedAt                      string          `json:"createdAt"`
	Status                         string          `json:"status"`
	ReferencePeriodStart           string          `json:"referencePeriodStart,omitempty"`
	ReferencePeriodEnd             string          `json:"referencePeriodEnd,omitempty"`
	SecondaryEmissionFactorSources []SourceRef     `json:"secondaryEmissionFactorSources,omitempty"`
	Comment                        string          `json:"comment,omitempty"`
	TOCID                          string          `json:"tocId"`
	Description                    string          `json:"description,omitempty"`
	Mode                           string          `json:"mode"`
	LoadFactor                     Decimal         `json:"loadFactor,omitempty"`
	EmptyDistanceFactor            Decimal         `json:"emptyDistanceFactor,omitempty"`
	TemperatureControl             string          `json:"temperatureControl,omitempty"`
	AirShippingOption              string          `json:"airShippingOption,omitempty"`
	FlightLength                   string          `json:"flightLength,omitempty"`
	EnergyCarriers                 []EnergyCarrier `json:"energyCarriers,omitempty"` // mandatory; empty only when unknown (a Gap)
	CO2eIntensityWTW               Decimal         `json:"co2eIntensityWTW"`
	CO2eIntensityTTW               Decimal         `json:"co2eIntensityTTW,omitempty"`
	TransportActivityUnit          string          `json:"transportActivityUnit"`
}

// HOC is a hub operation category (iLEAP 6.4).
type HOC struct {
	SpecVersion                    string          `json:"specVersion"`
	CompanyName                    string          `json:"companyName,omitempty"`
	CompanyIDs                     []string        `json:"companyIds,omitempty"`
	CreatedAt                      string          `json:"createdAt"`
	Status                         string          `json:"status"`
	ReferencePeriodStart           string          `json:"referencePeriodStart,omitempty"`
	ReferencePeriodEnd             string          `json:"referencePeriodEnd,omitempty"`
	SecondaryEmissionFactorSources []SourceRef     `json:"secondaryEmissionFactorSources,omitempty"`
	Comment                        string          `json:"comment,omitempty"`
	HOCID                          string          `json:"hocId"`
	Description                    string          `json:"description,omitempty"`
	HubType                        string          `json:"hubType,omitempty"` // mandatory; empty only when unknown (a Gap)
	TemperatureControl             string          `json:"temperatureControl,omitempty"`
	EnergyCarriers                 []EnergyCarrier `json:"energyCarriers,omitempty"`
	CO2eIntensityWTW               Decimal         `json:"co2eIntensityWTW"`
	CO2eIntensityTTW               Decimal         `json:"co2eIntensityTTW,omitempty"`
	HubActivityUnit                string          `json:"hubActivityUnit"`
}

// SourceRef names a secondary emission factor source.
type SourceRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// GLECDistance is a distance in km by GLEC distance type (iLEAP 6.7.1).
type GLECDistance struct {
	Actual Decimal `json:"actual,omitempty"`
	GCD    Decimal `json:"gcd,omitempty"`
	SFD    Decimal `json:"sfd,omitempty"`
}

// Location is a place (iLEAP 6.7.2); city and country are mandatory.
type Location struct {
	City    string  `json:"city"`
	Country string  `json:"country"`
	LOCODE  string  `json:"locode,omitempty"`
	Lat     Decimal `json:"lat,omitempty"`
	Lng     Decimal `json:"lng,omitempty"`
}

// EnergyCarrier is one energy carrier of a TOC or HOC (iLEAP 6.7.6).
type EnergyCarrier struct {
	EnergyCarrier         string      `json:"energyCarrier"`
	RelativeShare         Decimal     `json:"relativeShare"`
	Feedstocks            []Feedstock `json:"feedstocks,omitempty"`
	EnergyConsumption     Decimal     `json:"energyConsumption,omitempty"`
	EnergyConsumptionUnit string      `json:"energyConsumptionUnit,omitempty"`
	EmissionFactorWTW     Decimal     `json:"emissionFactorWTW,omitempty"`
	EmissionFactorTTW     Decimal     `json:"emissionFactorTTW,omitempty"`
}

// Feedstock is one feedstock of an energy carrier (iLEAP 6.7.8).
type Feedstock struct {
	Feedstock        string  `json:"feedstock"`
	FeedstockShare   Decimal `json:"feedstockShare,omitempty"`
	RegionProvenance string  `json:"regionProvenance,omitempty"`
}

// Gap is a property iLEAP requires that the data doesn't give.
type Gap struct {
	Object string `json:"object"` // shipment, tce, toc or hoc
	ID     string `json:"id"`
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Export is a set of iLEAP objects and what they lack.
type Export struct {
	SpecVersion        string              `json:"ileap_spec_version"`
	Conformant         bool                `json:"conformant"`
	ShipmentFootprints []ShipmentFootprint `json:"shipment_footprints,omitempty"`
	TOCs               []TOC               `json:"tocs,omitempty"`
	HOCs               []HOC               `json:"hocs,omitempty"`
	Gaps               []Gap               `json:"gaps,omitempty"`
	Notes              []string            `json:"notes,omitempty"`
}

// Values iLEAP allows (iLEAP 6.3, 6.4, 6.7).
const (
	StatusActive = "Active"

	UnitTkm    = "tkm"
	UnitTEUkm  = "TEUkm"
	UnitTonnes = "tonnes"
	UnitTEU    = "TEU"

	TempAmbient      = "ambient"
	TempRefrigerated = "refrigerated"
	TempMixed        = "mixed"
)
