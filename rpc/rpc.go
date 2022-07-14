package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"truenft.tech/tnft-stratum/v2/pool"
)

type RPCClient struct {
	sync.RWMutex
	sickRate         int64
	successRate      int64
	Accepts          int64
	Rejects          int64
	LastSubmissionAt int64
	FailsCount       int64
	Url              *url.URL
	login            string
	password         string
	Name             string
	sick             bool
	client           *http.Client
	info             atomic.Value
}

type GetBlockTemplateReply struct {
	Difficulty     int64  `json:"difficulty"`
	Height         int64  `json:"height"`
	Blob           string `json:"blocktemplate_blob"`
	ReservedOffset int    `json:"reserved_offset"`
	PrevHash       string `json:"prev_hash"`
	SeedHash       string `json:"seed_hash"`
	NextSeedHash   string `json:"next_seed_hash"`
}

type GetInfoReply struct {
	IncomingConnections int64  `json:"incoming_connections_count"`
	OutgoingConnections int64  `json:"outgoing_connections_count"`
	Height              int64  `json:"height"`
	TxPoolSize          int64  `json:"tx_pool_size"`
	Status              string `json:"status"`
}

type GetBlockHashReply struct {
	Hash string `json:"hash"`
}

type ValidateAddressReply struct {
	IsValid bool `json:"isvalid"`
	IsMine  bool `json:"ismine"`
}

type JSONRpcResp struct {
	Id     *json.RawMessage       `json:"id"`
	Result *json.RawMessage       `json:"result"`
	Error  map[string]interface{} `json:"error"`
}

func NewRPCClient(cfg *pool.Upstream) (*RPCClient, error) {
	rawUrl := fmt.Sprintf("http://%s:%v/json_rpc", cfg.Host, cfg.Port)
	url, err := url.Parse(rawUrl)
	if err != nil {
		return nil, err
	}
	rpcClient := &RPCClient{Name: cfg.Name, Url: url, login: cfg.User, password: cfg.Password}
	timeout, _ := time.ParseDuration(cfg.Timeout)
	rpcClient.client = &http.Client{
		Timeout: timeout,
	}
	return rpcClient, nil
}

func (r *RPCClient) GetBlockTemplate(reserveSize int, address string) (*GetBlockTemplateReply, error) {
	params := map[string]interface{}{"reserve_size": reserveSize, "wallet_address": address}
	rpcResp, err := r.doPost(r.Url.String(), "getblocktemplate", params)
	var reply *GetBlockTemplateReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) GetInfo() (*GetInfoReply, error) {
	params := make(map[string]interface{})
	rpcResp, err := r.doPost(r.Url.String(), "get_info", params)
	var reply *GetInfoReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) SubmitBlock(hash string) (*JSONRpcResp, error) {
	return r.doPost(r.Url.String(), "submitblock", []string{hash})
}

func (r *RPCClient) ValidateAddress(addr string) (*JSONRpcResp, error) {
	return r.doPost(r.Url.String(), "validateaddress", []string{addr})
}

func (r *RPCClient) GetBlockHash(height int) (*GetBlockHashReply, error) {
	params := make(map[string]interface{})
	rpcResp, err := r.doPost(r.Url.String(), "getblockhash", params)
	var reply *GetBlockHashReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) doPost(url, method string, params interface{}) (*JSONRpcResp, error) {
	jsonReq := map[string]interface{}{"jsonrpc": "2.0", "id": 0, "method": method, "params": params}
	data, _ := json.Marshal(jsonReq)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Length", (string)(len(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(r.login, r.password)
	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	/*
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return nil, errors.New(resp.Status)
		}
	*/

	var rpcResp *JSONRpcResp
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}
	if rpcResp.Error != nil {
		r.markSick()
		return nil, errors.New(rpcResp.Error["message"].(string))
	}
	return rpcResp, err
}

func (r *RPCClient) Check(reserveSize int, address string) (bool, error) {
	_, err := r.GetBlockTemplate(reserveSize, address)
	if err != nil {
		return false, err
	}
	r.markAlive()
	return !r.Sick(), nil
}

func (r *RPCClient) Sick() bool {
	r.RLock()
	defer r.RUnlock()
	return r.sick
}

func (r *RPCClient) markSick() {
	r.Lock()
	if !r.sick {
		atomic.AddInt64(&r.FailsCount, 1)
	}
	r.sickRate++
	r.successRate = 0
	if r.sickRate >= 5 {
		r.sick = true
	}
	r.Unlock()
}

func (r *RPCClient) markAlive() {
	r.Lock()
	r.successRate++
	if r.successRate >= 5 {
		r.sick = false
		r.sickRate = 0
		r.successRate = 0
	}
	r.Unlock()
}

func (r *RPCClient) UpdateInfo() (*GetInfoReply, error) {
	info, err := r.GetInfo()
	if err == nil {
		r.info.Store(info)
	}
	return info, err
}

func (r *RPCClient) Info() *GetInfoReply {
	reply, _ := r.info.Load().(*GetInfoReply)
	return reply
}
