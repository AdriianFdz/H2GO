package contracts

import (
	"encoding/json"
	"errors"
	"h2go-chaincodes/models"
	"time"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type RedemptionContract struct {
	contractapi.Contract
}

func (rdpc *RedemptionContract) RedeemGdOs(
	ctx contractapi.TransactionContextInterface,
	ownerID string,
	assetType string,
	gdoIDs []string) error {

	if len(gdoIDs) == 0 {
		return errors.New("at least one GdO ID must be provided")
	}

	assetTypeEnum, err := models.ParseAssetType(assetType)
	if err != nil {
		return err
	}

	validatedGdOs := make(map[string]*models.GdO)

	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return err
	}
	currentTime := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos))

	for _, gdoID := range gdoIDs {
		gdo, err := rdpc.GetGdO(ctx, gdoID)
		if err != nil {
			return errors.New("failed to get GdO " + gdoID + ": " + err.Error())
		}

		if gdo.OwnerID != ownerID {
			return errors.New("GdO " + gdoID + " does not belong to owner " + ownerID + ", current owner is " + gdo.OwnerID)
		}

		if gdo.Status != models.GdoActive {
			return errors.New("GdO " + gdoID + " is not available for redemption, current status: " + string(gdo.Status))
		}

		expiryTime, err := time.Parse(time.RFC3339, gdo.ExpiryDate)
		if err != nil {
			return errors.New("failed to parse expiry date for GdO " + gdoID + ": " + err.Error())
		}

		if currentTime.After(expiryTime) {
			return errors.New("GdO " + gdoID + " has expired")
		}

		if gdo.AssetType != assetTypeEnum {
			return errors.New("GdO " + gdoID + " has asset type " + string(gdo.AssetType) + " but expected " + assetType)
		}

		validatedGdOs[gdoID] = gdo
	}

	ownerBalance, err := ctx.GetStub().GetState(ownerID)
	if err != nil {
		return errors.New("failed to read owner balance from world state")
	}
	if ownerBalance == nil {
		return errors.New("owner balance does not exist")
	}

	var productorBalanceRecord models.ProductorBalance
	err = json.Unmarshal(ownerBalance, &productorBalanceRecord)
	if err != nil {
		return err
	}

	gdoIDsSet := make(map[string]struct{})
	for _, gdoID := range gdoIDs {
		gdoIDsSet[gdoID] = struct{}{}
	}

	var availableGdOs []models.GdO
	var unavailableGdOs []models.GdO

	if assetTypeEnum == models.Electricity {
		availableGdOs = productorBalanceRecord.GdOS.Electricity.Available
		unavailableGdOs = productorBalanceRecord.GdOS.Electricity.Unavailable
	} else if assetTypeEnum == models.H2 {
		availableGdOs = productorBalanceRecord.GdOS.H2.Available
		unavailableGdOs = productorBalanceRecord.GdOS.H2.Unavailable
	}

	updatedAvailable := make([]models.GdO, 0)
	for _, gdo := range availableGdOs {
		if _, shouldRedeem := gdoIDsSet[gdo.GdoID]; shouldRedeem {
			validatedGdO := validatedGdOs[gdo.GdoID]
			validatedGdO.Status = models.GdoUsed
			
			unavailableGdOs = append(unavailableGdOs, *validatedGdO)

			gdoJSON, err := json.Marshal(validatedGdO)
			if err != nil {
				return err
			}
			err = ctx.GetStub().PutState(validatedGdO.GdoID, gdoJSON)
			if err != nil {
				return err
			}
		} else {
			updatedAvailable = append(updatedAvailable, gdo)
		}
	}

	if assetTypeEnum == models.Electricity {
		productorBalanceRecord.GdOS.Electricity.Available = updatedAvailable
		productorBalanceRecord.GdOS.Electricity.Unavailable = unavailableGdOs
	} else if assetTypeEnum == models.H2 {
		productorBalanceRecord.GdOS.H2.Available = updatedAvailable
		productorBalanceRecord.GdOS.H2.Unavailable = unavailableGdOs
	}

	updatedOwnerBalanceBytes, err := json.Marshal(productorBalanceRecord)
	if err != nil {
		return err
	}

	err = ctx.GetStub().PutState(ownerID, updatedOwnerBalanceBytes)
	if err != nil {
		return err
	}

	return nil
}

func (rdpc *RedemptionContract) GetGdO(ctx contractapi.TransactionContextInterface, gdoID string) (*models.GdO, error) {
	gdoBytes, err := ctx.GetStub().GetState(gdoID)
	if err != nil {
		return nil, errors.New("failed to read GdO from world state")
	}
	if gdoBytes == nil {
		return nil, errors.New("GdO does not exist")
	}

	var gdoRecord models.GdO
	err = json.Unmarshal(gdoBytes, &gdoRecord)
	if err != nil {
		return nil, err
	}

	return &gdoRecord, nil
}

func (rdpc *RedemptionContract) GetProducerBalance(ctx contractapi.TransactionContextInterface, ownerID string) (*models.ProductorBalance, error) {
	ownerBalance, err := ctx.GetStub().GetState(ownerID)
	if err != nil {
		return nil, errors.New("failed to read owner balance from world state")
	}
	if ownerBalance == nil {
		return nil, errors.New("owner balance does not exist")
	}

	var productorBalanceRecord models.ProductorBalance
	
}

func (rdpc *RedemptionContract) CreateTradeRequest(
	ctx contractapi.TransactionContextInterface,
	producerID string,
	targetProducerID string,
	assetType string,
	amount int64) (string, error) {

	assetTypeEnum, err := models.ParseAssetType(assetType)
	if err != nil {
		return "", err
	}

	requestID := ctx.GetStub().GetTxID()

	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return "", err
	}
	createdAt := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos)).Format(time.RFC3339)

	request := models.TradeRequest{
		DocType:    "REQUEST_TO_TRADE_GdOS",
		TradeID:    requestID,
		ProducerID: producerID,
		TargetID:   targetProducerID,
		AssetType:  assetTypeEnum,
		Amount:     float64(amount),
		Status:     models.RequestPending,
		CreatedAt: createdAt,
	}

	requestJSON, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	err = ctx.GetStub().PutState(requestID, requestJSON)
	if err != nil {
		return "", err
	}

	return requestID, nil
}

func (rdpc *RedemptionContract) GetAllTradeRequests(
	ctx contractapi.TransactionContextInterface) ([]*models.TradeRequest, error) {

	resultsIterator, err := ctx.GetStub().GetStateByRange("", "")
	if err != nil {
		return nil, err
	}
	defer resultsIterator.Close()

	var tradeRequests []*models.TradeRequest
	for resultsIterator.HasNext() {
		queryResponse, err := resultsIterator.Next()
		if err != nil {
			return nil, err
		}

		var request models.TradeRequest
		err = json.Unmarshal(queryResponse.Value, &request)
		if err != nil {
			continue
		}

		if request.DocType == "REQUEST_TO_TRADE_GdOS" {
			tradeRequests = append(tradeRequests, &request)
		}
	}

	return tradeRequests, nil
}

func (rdpc *RedemptionContract) GetReceivedTradeRequests(
	ctx contractapi.TransactionContextInterface,
	targetProducerID string) ([]*models.TradeRequest, error) {

	allTradeRequests, err := rdpc.GetAllTradeRequests(ctx)
	if err != nil {
		return nil, err
	}

	var receivedRequests []*models.TradeRequest
	for _, request := range allTradeRequests {
		if request.TargetID == targetProducerID {
			receivedRequests = append(receivedRequests, request)
		}
	}

	return receivedRequests, nil
}

func (rdpc *RedemptionContract) GetSentTradeRequests(
	ctx contractapi.TransactionContextInterface,
	producerID string) ([]*models.TradeRequest, error) {

	allTradeRequests, err := rdpc.GetAllTradeRequests(ctx)
	if err != nil {
		return nil, err
	}

	var sentRequests []*models.TradeRequest
	for _, request := range allTradeRequests {
		if request.ProducerID == producerID {
			sentRequests = append(sentRequests, request)
		}
	}

	return sentRequests, nil
}

func (rdpc *RedemptionContract) GetReceivedTradeRequestsByStatus(
	ctx contractapi.TransactionContextInterface,
	targetProducerID string,
	status string) ([]*models.TradeRequest, error) {

	statusEnum, err := models.ParseRequestStatus(status)
	if err != nil {
		return nil, err
	}

	allTradeRequests, err := rdpc.GetAllTradeRequests(ctx)
	if err != nil {
		return nil, err
	}

	var receivedRequests []*models.TradeRequest
	for _, request := range allTradeRequests {
		if request.TargetID == targetProducerID && request.Status == statusEnum {
			receivedRequests = append(receivedRequests, request)
		}
	}

	return receivedRequests, nil
}

func (rdpc *RedemptionContract) GetSentTradeRequestsByStatus(
	ctx contractapi.TransactionContextInterface,
	producerID string,
	status string) ([]*models.TradeRequest, error) {

	statusEnum, err := models.ParseRequestStatus(status)
	if err != nil {
		return nil, err
	}

	allTradeRequests, err := rdpc.GetAllTradeRequests(ctx)
	if err != nil {
		return nil, err
	}

	var sentRequests []*models.TradeRequest
	for _, request := range allTradeRequests {
		if request.ProducerID == producerID && request.Status == statusEnum {
			sentRequests = append(sentRequests, request)
		}
	}

	return sentRequests, nil
}

func (rdpc *RedemptionContract) GetTradeRequest(
	ctx contractapi.TransactionContextInterface,
	tradeID string) (*models.TradeRequest, error) {
	tradeBytes, err := ctx.GetStub().GetState(tradeID)
	if err != nil {
		return nil, errors.New("failed to read trade request from world state")
	}
	if tradeBytes == nil {
		return nil, errors.New("trade request does not exist")
	}

	var tradeRequest models.TradeRequest
	err = json.Unmarshal(tradeBytes, &tradeRequest)
	if err != nil {
		return nil, err
	}

	return &tradeRequest, nil
}

func (rdpc *RedemptionContract) AcceptTradeRequest(
	ctx contractapi.TransactionContextInterface,
	producerID string,
	tradeID string,
	gdosToExchange []string,
) error {
	trade, err := rdpc.GetTradeRequest(ctx, tradeID)
	if err != nil {
		return err
	}

	if trade.TargetID != producerID {
		return errors.New("only the target producer can accept the trade request")
	}

	if trade.Status != models.RequestPending {
		return errors.New("trade request is not in PENDING status, current status: " + string(trade.Status))
	}

	if len(gdosToExchange) != int(trade.Amount) {
		return errors.New("number of GdOs to exchange must match the amount specified in the trade request")
	}

	// Validate all GdOs and create set to avoid nested loops
	gdoIDsSet := make(map[string]struct{}, len(gdosToExchange))
	for _, gdoID := range gdosToExchange {
		gdo, err := rdpc.GetGdO(ctx, gdoID)
		if err != nil {
			return err
		}
		if gdo.OwnerID != producerID {
			return errors.New("producer " + producerID + " does not own GdO " + gdoID)
		}
		if gdo.AssetType != trade.AssetType {
			return errors.New("GdO " + gdoID + " asset type does not match trade request asset type")
		}
		if gdo.Status != models.GdoActive {
			return errors.New("GdO " + gdoID + " is not available for trade")
		}
		gdoIDsSet[gdoID] = struct{}{}
	}

	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return err
	}
	processedAt := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos)).Format(time.RFC3339)

	approverID, err := ctx.GetClientIdentity().GetID()
	if err != nil {
		return errors.New("failed to get client identity: " + err.Error())
	}

	targetBalanceBytes, err := ctx.GetStub().GetState(trade.TargetID)
	if err != nil {
		return errors.New("failed to read target balance: " + err.Error())
	}
	if targetBalanceBytes == nil {
		return errors.New("target producer balance does not exist")
	}
	var targetBalance models.ProductorBalance
	err = json.Unmarshal(targetBalanceBytes, &targetBalance)
	if err != nil {
		return err
	}

	producerBalanceBytes, err := ctx.GetStub().GetState(trade.ProducerID)
	if err != nil {
		return errors.New("failed to read producer balance: " + err.Error())
	}
	var producerBalance models.ProductorBalance
	if producerBalanceBytes == nil {
		// Initialize
		producerBalance = models.ProductorBalance{
			TransactionType: "gdoBalance",
			ProducerID:      trade.ProducerID,
			GdOS: models.GdOsByAssetType{
				Electricity: models.GdOsByStatus{
					Available:   make([]models.GdO, 0),
					Unavailable: make([]models.GdO, 0),
				},
				H2: models.GdOsByStatus{
					Available:   make([]models.GdO, 0),
					Unavailable: make([]models.GdO, 0),
				},
			},
		}
	} else {
		err = json.Unmarshal(producerBalanceBytes, &producerBalance)
		if err != nil {
			return err
		}
	}

	gdosToTransfer := make([]models.GdO, 0)
	updatedTargetAvailable := make([]models.GdO, 0)

	var targetAvailable []models.GdO
	if trade.AssetType == models.Electricity {
		targetAvailable = targetBalance.GdOS.Electricity.Available
	} else {
		targetAvailable = targetBalance.GdOS.H2.Available
	}

	// Swap ownership
	for _, gdo := range targetAvailable {
		if _, exists := gdoIDsSet[gdo.GdoID]; exists {
			gdo.OwnerID = trade.ProducerID
			gdosToTransfer = append(gdosToTransfer, gdo)

			gdoJSON, err := json.Marshal(gdo)
			if err != nil {
				return err
			}
			err = ctx.GetStub().PutState(gdo.GdoID, gdoJSON)
			if err != nil {
				return err
			}
		} else {
			updatedTargetAvailable = append(updatedTargetAvailable, gdo)
		}
	}

	if trade.AssetType == models.Electricity {
		targetBalance.GdOS.Electricity.Available = updatedTargetAvailable
	} else {
		targetBalance.GdOS.H2.Available = updatedTargetAvailable
	}

	if trade.AssetType == models.Electricity {
		producerBalance.GdOS.Electricity.Available = append(producerBalance.GdOS.Electricity.Available, gdosToTransfer...)
	} else {
		producerBalance.GdOS.H2.Available = append(producerBalance.GdOS.H2.Available, gdosToTransfer...)
	}

	// Save both balances
	targetBalanceJSON, err := json.Marshal(targetBalance)
	if err != nil {
		return err
	}
	err = ctx.GetStub().PutState(trade.TargetID, targetBalanceJSON)
	if err != nil {
		return err
	}

	producerBalanceJSON, err := json.Marshal(producerBalance)
	if err != nil {
		return err
	}
	err = ctx.GetStub().PutState(trade.ProducerID, producerBalanceJSON)
	if err != nil {
		return err
	}

	// Update trade request
	trade.Status = models.RequestApproved
	trade.ApproverID = approverID
	trade.ProcessedAt = processedAt
	trade.GdOs = gdosToTransfer

	tradeJSON, err := json.Marshal(trade)
	if err != nil {
		return err
	}
	err = ctx.GetStub().PutState(tradeID, tradeJSON)
	if err != nil {
		return err
	}

	return nil
}

func (rdpc *RedemptionContract) RejectTradeRequest(
	ctx contractapi.TransactionContextInterface,
	producerID string,
	tradeID string,
) error {
	trade, err := rdpc.GetTradeRequest(ctx, tradeID)
	if err != nil {
		return err
	}

	if trade.TargetID != producerID {
		return errors.New("only the target producer can reject the trade request")
	}

	if trade.Status != models.RequestPending {
		return errors.New("trade request is not in PENDING status, current status: " + string(trade.Status))
	}

	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return err
	}
	processedAt := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos)).Format(time.RFC3339)

	approverID, err := ctx.GetClientIdentity().GetID()
	if err != nil {
		return errors.New("failed to get client identity: " + err.Error())
	}

	trade.Status = models.RequestRejected
	trade.ApproverID = approverID
	trade.ProcessedAt = processedAt

	tradeJSON, err := json.Marshal(trade)
	if err != nil {
		return err
	}
	err = ctx.GetStub().PutState(tradeID, tradeJSON)
	if err != nil {
		return err
	}

	return nil
}

func (rdpc *RedemptionContract) GetExpiredGdOs(ctx contractapi.TransactionContextInterface) ([]string, error) {
	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return nil, err
	}
	currentTime := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos))

	resultsIterator, err := ctx.GetStub().GetStateByRange("", "")
	if err != nil {
		return nil, err
	}
	defer resultsIterator.Close()

	var expiredGdoIDs []string
	for resultsIterator.HasNext() {
		queryResponse, err := resultsIterator.Next()
		if err != nil {
			return nil, err
		}

		var gdo models.GdO
		err = json.Unmarshal(queryResponse.Value, &gdo)
		if err != nil {
			continue
		}

		if gdo.DocType != "GdO" || gdo.Status != models.GdoActive {
			continue
		}

		expiryTime, err := time.Parse(time.RFC3339, gdo.ExpiryDate)
		if err != nil {
			continue
		}

		if currentTime.After(expiryTime) {
			expiredGdoIDs = append(expiredGdoIDs, gdo.GdoID)
		}
	}

	return expiredGdoIDs, nil
}

func (rdpc *RedemptionContract) SetGdoAsExpired(ctx contractapi.TransactionContextInterface, gdoID string) error {
	gdo, err := rdpc.GetGdO(ctx, gdoID)
	if err != nil {
		return err
	}

	if gdo.Status != models.GdoActive {
		return nil
	}

	// Get deterministic timestamp from transaction
	txTimestamp, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return err
	}
	currentTime := time.Unix(txTimestamp.Seconds, int64(txTimestamp.Nanos))

	expiryTime, err := time.Parse(time.RFC3339, gdo.ExpiryDate)
	if err != nil {
		return errors.New("failed to parse expiry date for GdO " + gdoID + ": " + err.Error())
	}

	if !currentTime.After(expiryTime) {
		return errors.New("GdO " + gdoID + " has not yet expired")
	}

	// The current owner is always gdo.OwnerID
	// (updated during trades in AcceptTradeRequest)
	ownerID := gdo.OwnerID

	// 1. Update the GdO status in world state
	gdo.Status = models.GdoExpired
	gdoJSON, err := json.Marshal(gdo)
	if err != nil {
		return err
	}
	err = ctx.GetStub().PutState(gdo.GdoID, gdoJSON)
	if err != nil {
		return err
	}

	// 2. Update the ProductorBalance: move GdO from Available to Unavailable
	ownerBalanceBytes, err := ctx.GetStub().GetState(ownerID)
	if err != nil {
		return errors.New("failed to read owner balance: " + err.Error())
	}
	if ownerBalanceBytes == nil {
		return errors.New("owner balance does not exist for " + ownerID)
	}

	var balance models.ProductorBalance
	err = json.Unmarshal(ownerBalanceBytes, &balance)
	if err != nil {
		return err
	}

	var availableGdOs *[]models.GdO
	var unavailableGdOs *[]models.GdO

	if gdo.AssetType == models.Electricity {
		availableGdOs = &balance.GdOS.Electricity.Available
		unavailableGdOs = &balance.GdOS.Electricity.Unavailable
	} else if gdo.AssetType == models.H2 {
		availableGdOs = &balance.GdOS.H2.Available
		unavailableGdOs = &balance.GdOS.H2.Unavailable
	} else {
		return errors.New("unknown asset type: " + string(gdo.AssetType))
	}

	// Remove from Available, add to Unavailable with EXPIRED status
	updatedAvailable := make([]models.GdO, 0, len(*availableGdOs))
	found := false
	for _, g := range *availableGdOs {
		if g.GdoID == gdoID {
			found = true
			g.Status = models.GdoExpired
			*unavailableGdOs = append(*unavailableGdOs, g)
		} else {
			updatedAvailable = append(updatedAvailable, g)
		}
	}
	*availableGdOs = updatedAvailable

	if !found {
		return errors.New("GdO " + gdoID + " not found in owner's available balance")
	}

	updatedBalanceJSON, err := json.Marshal(balance)
	if err != nil {
		return err
	}
	return ctx.GetStub().PutState(ownerID, updatedBalanceJSON)
}