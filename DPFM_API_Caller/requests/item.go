package requests

type Item struct {
	PurchaseRequisition    		int     `json:"PurchaseRequisition"`
	PurchaseRequisitionItem		int     `json:"PurchaseRequisitionItem"`
	IsReleased        			*bool   `json:"IsReleased"`
}
