package requests

type Header struct {
	PurchaseRequisition            int     `json:"PurchaseRequisition"`
	IsReleased          	   	   *bool   `json:"IsReleased"`
}
