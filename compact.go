package nvd

// fields for compact representation of a CVE

type CVEIndex struct {
	ID          string `boltholdKey:"ID"`
	Description string
}

type cveDesc struct {
	CVEITems []cveInnerItem `json:"CVE_Items"`
}

type cveInnerItem struct {
	CVE cveData `json:"cve"`
}

type cveData struct {
	Description descData `json:"description"`
	Meta        dataMeta `json:"CVE_data_meta"`
}

type dataMeta struct {
	ID string `json:"ID"`
}

type descData struct {
	Data []dataItem `json:"description_data"`
}

type dataItem struct {
	Lang  string `json:"lang"`
	Value string `json:"value"`
}
