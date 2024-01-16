package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/blocklessnetwork/b7s/api"
	"github.com/blocklessnetwork/b7s/models/blockless"
	"github.com/blocklessnetwork/b7s/models/codes"
	"github.com/blocklessnetwork/b7s/models/execute"
	"github.com/blocklessnetwork/b7s/node/aggregate"
	"github.com/labstack/echo/v4"
)

// ExecuteRequest describes the payload for the REST API request for function execution.
type ExecuteRequest execute.Request

// ExecuteResponse describes the REST API response for function execution.
type ExecuteResponse struct {
	Code      codes.Code        `json:"code,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Message   string            `json:"message,omitempty"`
	Results   aggregate.Results `json:"results,omitempty"`
	Cluster   execute.Cluster   `json:"cluster,omitempty"`
}

// ExecuteResult represents the API representation of a single execution response.
// It is similar to the model in `execute.Result`, except it omits the usage information for now.
type ExecuteResult struct {
	Code      codes.Code            `json:"code,omitempty"`
	Result    execute.RuntimeOutput `json:"result,omitempty"`
	RequestID string                `json:"request_id,omitempty"`
}

func createExecutor(a api.API) func(ctx echo.Context) error {
	return func(ctx echo.Context) error {

		// Unpack the API request.
		var req ExecuteRequest
		err := ctx.Bind(&req)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Errorf("could not unpack request: %w", err))
		}
		fmt.Println("Executing inference function: ", req.FunctionID)

		// Get the execution result.
		code, id, results, cluster, err := a.Node.ExecuteFunction(ctx.Request().Context(), execute.Request(req))
		if err != nil {
			a.Log.Warn().Str("function", req.FunctionID).Err(err).Msg("node failed to execute function")
		}

		// Transform the node response format to the one returned by the API.
		res := ExecuteResponse{
			Code:      code,
			RequestID: id,
			Results:   aggregate.Aggregate(results),
			Cluster:   cluster,
		}

		// Communicate the reason for failure in these cases.
		if errors.Is(err, blockless.ErrRollCallTimeout) || errors.Is(err, blockless.ErrExecutionNotEnoughNodes) {
			res.Message = err.Error()
		}
		
		// Send the inferences to the appchain
		client, err := NewAppChainClient()
		if err != nil {
			return fmt.Errorf("could not create appchain client: %w", err)
		}
		fmt.Println("Sending inferences to appchain")
		inferences := client.SendInferencesToAppChain(1, res.Results)
		fmt.Println("Inferences sent to appchain :: ", inferences)
		// Get the dependencies for the weights calculation
		ethPrice, latestWeights := client.GetWeightsCalcDependencies(inferences)

		fmt.Println("ETH price: ", ethPrice)
		fmt.Println("Latest weights: ", latestWeights)
		fmt.Println("Inferences: ", inferences)

		// Format the payload for the weights calculation
		var weightsReq map[string]interface{} = make(map[string]interface{})
		weightsReq["eth_price"] = ethPrice
		weightsReq["inferences"] = inferences
		weightsReq["latest_weights"] = latestWeights
		payload, err := json.Marshal(weightsReq)
		if err != nil {
			fmt.Println("Error marshalling weights request: ", err)
		}
		payloadCopy := string(payload)
		fmt.Println("Payload: ", payloadCopy)
	
		// Calculate the weights
		calcWeightsReq := execute.Request{
			FunctionID: "bafybeif5cu26lo7wh7pdn2tuv6un3c3kdxberxznlgnvntkftkpkiesqdi",
			Method:     "eth-price-processing.wasm",
			Config: execute.Config{
				Stdin:  &payloadCopy,
			},
		}
		fmt.Println("Executing weight adjusment function: ", calcWeightsReq.FunctionID)

		// Get the execution result.
		_, _, weightsResults, _, err := a.Node.ExecuteFunction(ctx.Request().Context(), execute.Request(calcWeightsReq))
		if err != nil {
			a.Log.Warn().Str("function", req.FunctionID).Err(err).Msg("node failed to execute function")
		}
		fmt.Println("Weights results: ", weightsResults)

		// Transform the node response format to the one returned by the API.
		client.SendUpdatedWeights(aggregate.Aggregate(weightsResults))

		// Send the response.
		return ctx.JSON(http.StatusOK, res)
	}
}

// {"eth_price": 2530.5,"inferences":[{"worker":"upt16ar7k93c6razqcuvxdauzdlaz352sfjp2rpj3i","inference":2443}],"latest_weights":{"upt16ar7k93c6razqcuvxdauzdlaz352sfjp2rpj3i": 0.3}}
// {"eth_price": 555, "inferences": [{"worker": "worker1", "inference": 560}, {"worker": "worker2", "inference": 550}], "latest_weights": {"worker1": 0.9, "worker2": 0.8}}