package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type Client struct {
	Endpoint string
}

func NewClient(endpoint string) *Client {
	return &Client{Endpoint: endpoint}
}

func (c *Client) GetCode(address, blk string) ([]byte, error) {
	// try to convert block into number
	blkNumber, ok := new(big.Int).SetString(strings.TrimLeft(blk, "0x"), 16)
	if !ok || blkNumber.Cmp(big.NewInt(0)) <= 0 {
		blk = "latest"
	}

	params := []interface{}{
		address, blk,
	}

	rpcResp, err := rpcPost(c.Endpoint, "eth_getCode", params)
	if err != nil {
		return nil, err
	}

	resultB, _ := rpcResp.Result.MarshalJSON()

	var result string
	err = json.Unmarshal(resultB, &result)
	if err != nil {
		return nil, err
	}

	return hexutil.MustDecode(result), nil
}

func (c *Client) GetStorageAt(address, position, blk string) (common.Hash, error) {
	blkNumber, ok := new(big.Int).SetString(strings.TrimLeft(blk, "0x"), 16)
	if !ok || blkNumber.Cmp(big.NewInt(0)) <= 0 {
		blk = "latest"
	}

	params := []interface{}{
		address, position, blk,
	}

	rpcResp, err := rpcPost(c.Endpoint, "eth_getStorageAt", params)
	if err != nil {
		return common.Hash{}, err
	}

	resultB, _ := rpcResp.Result.MarshalJSON()

	var result string
	err = json.Unmarshal(resultB, &result)
	if err != nil {
		return common.Hash{}, err
	}

	return common.HexToHash(result), nil
}

func (c *Client) GetCodeAndStorageAt(address, position, blk string) ([]byte, common.Hash, error) {
	// fetch code and storage
	code, err := c.GetCode(address, blk)
	if err != nil {
		return nil, common.Hash{}, err
	}

	storage, err := c.GetStorageAt(address, position, blk)
	if err != nil {
		return nil, common.Hash{}, err
	}

	return code, storage, nil
}

func (c *Client) GetBalance(address, blk string) (*big.Int, error) {
	blkNumber, ok := new(big.Int).SetString(strings.TrimLeft(blk, "0x"), 16)
	if !ok || blkNumber.Cmp(big.NewInt(0)) <= 0 {
		blk = "latest"
	}

	params := []interface{}{
		address, blk,
	}

	rpcResp, err := rpcPost(c.Endpoint, "eth_getBalance", params)
	if err != nil {
		return nil, err
	}

	resultB, _ := rpcResp.Result.MarshalJSON()

	var result string
	err = json.Unmarshal(resultB, &result)
	if err != nil {
		return nil, err
	}

	balance, ok := new(big.Int).SetString(result[2:], 16)
	if !ok {
		return nil, fmt.Errorf("invalid balance received in response: %s", result)
	}

	return balance, nil
}

type RPCRequest struct {
	ID      int           `json:"id"`
	JSONRpc string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type RPCResponse struct {
	ID      int             `json:"id"`
	JSONRpc string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Err     *ErrResponse    `json:"error,omitempty"`
}

type ErrResponse struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func (e *ErrResponse) Error() string {
	return fmt.Sprintf(`{"code": "%d", "message": "%s"}`, e.Code, e.Message)
}

func rpcPost(rpcEndpoint, method string, params []interface{}) (*RPCResponse, error) {
	payload := RPCRequest{
		ID:      1,
		JSONRpc: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(&payload)
	if err != nil {
		return nil, err
	}
	body := bytes.NewBuffer(data)

	resp, err := http.Post(rpcEndpoint, "application/json", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result RPCResponse
	err = json.Unmarshal(b, &result)

	return &result, err
}
