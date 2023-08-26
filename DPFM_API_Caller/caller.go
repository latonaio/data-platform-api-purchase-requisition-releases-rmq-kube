package dpfm_api_caller

import (
	"context"
	dpfm_api_input_reader "data-platform-api-purchase-requisition-releases-rmq-kube/DPFM_API_Input_Reader"
	dpfm_api_output_formatter "data-platform-api-purchase-requisition-releases-rmq-kube/DPFM_API_Output_Formatter"
	"data-platform-api-purchase-requisition-releases-rmq-kube/config"

	"github.com/latonaio/golang-logging-library-for-data-platform/logger"
	database "github.com/latonaio/golang-mysql-network-connector"
	rabbitmq "github.com/latonaio/rabbitmq-golang-client-for-data-platform"
	"golang.org/x/xerrors"
)

type DPFMAPICaller struct {
	ctx  context.Context
	conf *config.Conf
	rmq  *rabbitmq.RabbitmqClient
	db   *database.Mysql
}

func NewDPFMAPICaller(
	conf *config.Conf, rmq *rabbitmq.RabbitmqClient, db *database.Mysql,
) *DPFMAPICaller {
	return &DPFMAPICaller{
		ctx:  context.Background(),
		conf: conf,
		rmq:  rmq,
		db:   db,
	}
}

func (c *DPFMAPICaller) AsyncReleases(
	accepter []string,
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	log *logger.Logger,
) (interface{}, []error) {
	var response interface{}
	switch input.APIType {
	case "releases":
		response = c.releaseSqlProcess(input, output, accepter, log)
	default:
		log.Error("unknown api type %s", input.APIType)
	}
	return response, nil
}

func (c *DPFMAPICaller) releaseSqlProcess(
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	accepter []string,
	log *logger.Logger,
) *dpfm_api_output_formatter.Message {
	var headerData *dpfm_api_output_formatter.Header
	itemData := make([]dpfm_api_output_formatter.Item, 0)
	for _, a := range accepter {
		switch a {
		case "Header":
			h, i := c.headerRelease(input, output, log)
			headerData = h
			if h == nil || i == nil {
				continue
			}
			itemData = append(itemData, *i...)
		case "Item":
			i := c.itemRelease(input, output, log)
			if i == nil {
				continue
			}
			itemData = append(itemData, *i...)
		}
	}

	return &dpfm_api_output_formatter.Message{
		Header: headerData,
		Item:   &itemData,
	}
}

func (c *DPFMAPICaller) headerRelease(
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	log *logger.Logger,
) (*dpfm_api_output_formatter.Header, *[]dpfm_api_output_formatter.Item) {
	sessionID := input.RuntimeSessionID

	header := c.HeaderRead(input, log)
	if header == nil {
		return nil, nil, nil, nil
	}
	header.IsReleased = input.PurchaseRequisition.IsReleased
	res, err := c.rmq.SessionKeepRequest(nil, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{"message": header, "function": "PurchaseRequisitionHeader", "runtime_session_id": sessionID})
	if err != nil {
		err = xerrors.Errorf("rmq error: %w", err)
		log.Error("%+v", err)
		return nil, nil, nil, nil
	}
	res.Success()
	if !checkResult(res) {
		output.SQLUpdateResult = getBoolPtr(false)
		output.SQLUpdateError = "Header Data cannot release"
		return nil, nil, nil, nil
	}
	// headerのリリースが取り消された時は子に影響を与えない
	if !*header.IsReleased {
		return header, nil, nil, nil
	}

	items := c.ItemsRead(input, log)
	for i := range *items {
		(*items)[i].IsReleased = input.PurchaseRequisition.IsReleased
		res, err := c.rmq.SessionKeepRequest(nil, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{"message": (*items)[i], "function": "PurchaseRequisitionItem", "runtime_session_id": sessionID})
		if err != nil {
			err = xerrors.Errorf("rmq error: %w", err)
			log.Error("%+v", err)
			return nil, nil, nil, nil
		}
		res.Success()
		if !checkResult(res) {
			output.SQLUpdateResult = getBoolPtr(false)
			output.SQLUpdateError = "Item Data cannot release"
			return nil, nil, nil, nil
		}
	}

	return header, items
}

func (c *DPFMAPICaller) itemRelease(
	input *dpfm_api_input_reader.SDC,
	output *dpfm_api_output_formatter.SDC,
	log *logger.Logger,
) *[]dpfm_api_output_formatter.Item {
	sessionID := input.RuntimeSessionID

	items := make([]dpfm_api_output_formatter.Item, 0)
	for _, v := range input.Header.Item {
		data := dpfm_api_output_formatter.Item{
			PurchaseRequisition:			input.Header.PurchaseRequisition,
			PurchaseRequisitionItem:		v.PurchaseRequisitionItem,
			IsReleased:						v.IsReleased,
		}
		res, err := c.rmq.SessionKeepRequest(nil, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{
			"message":            data,
			"function":           "PurchaseRequisitionItem",
			"runtime_session_id": sessionID,
		})
		if err != nil {
			err = xerrors.Errorf("rmq error: %w", err)
			log.Error("%+v", err)
			return nil
		}
		res.Success()
		if !checkResult(res) {
			output.SQLUpdateResult = getBoolPtr(false)
			output.SQLUpdateError = "Item Data cannot release"
			return nil
		}
	}
	// itemがリリース取り消しされた場合、headerのリリースも取り消す
	if !*input.Header.Item[0].IsReleased {
		header := c.HeaderRead(input, log)
		header.IsReleased = input.Header.Item[0].IsReleased
		res, err := c.rmq.SessionKeepRequest(nil, c.conf.RMQ.QueueToSQL()[0], map[string]interface{}{"message": header, "function": "PurchaseRequisitionHeader", "runtime_session_id": sessionID})
		if err != nil {
			err = xerrors.Errorf("rmq error: %w", err)
			log.Error("%+v", err)
			return nil
		}
		res.Success()
		if !checkResult(res) {
			output.SQLUpdateResult = getBoolPtr(false)
			output.SQLUpdateError = "Header Data cannot release"
			return nil
		}
	}

	return &items
}

func checkResult(msg rabbitmq.RabbitmqMessage) bool {
	data := msg.Data()
	d, ok := data["result"]
	if !ok {
		return false
	}
	result, ok := d.(string)
	if !ok {
		return false
	}
	return result == "success"
}

func getBoolPtr(b bool) *bool {
	return &b
}
