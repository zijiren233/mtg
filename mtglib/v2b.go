package mtglib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/exp/maps"
)

type V2bConfig struct {
	API      string `json:"api"`
	Token    string `json:"token"`
	NodeType string `json:"nodeType"`
	NodeID   uint   `json:"nodeId"`
}

type trafficStatsEntry struct {
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
}

type V2b struct {
	client   *http.Client
	config   *V2bConfig
	usersMap map[string]struct{}
	statsMap map[string]*trafficStatsEntry
	lock     sync.RWMutex
}

func newV2b(config *V2bConfig) *V2b {
	return &V2b{
		client:   &http.Client{},
		config:   config,
		usersMap: make(map[string]struct{}),
		statsMap: make(map[string]*trafficStatsEntry),
	}
}

type user struct {
	ID         int     `json:"id"`
	UUID       string  `json:"uuid"`
	SpeedLimit *uint32 `json:"speed_limit"`
}
type responseData struct {
	Users []*user `json:"users"`
}

func (v *V2b) Start(interval time.Duration) {
	go v.UpdateUsers(interval)
	go v.PushTrafficToV2boardInterval(interval)
}

func (v *V2b) getUserList(ctx context.Context, timeout time.Duration) ([]*user, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.config.API+"/api/v1/server/UniProxy/user", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Add("token", v.config.Token)
	q.Add("node_id", strconv.Itoa(int(v.config.NodeID)))
	q.Add("node_type", v.config.NodeType)
	req.URL.RawQuery = q.Encode()

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var responseData responseData
	err = json.NewDecoder(resp.Body).Decode(&responseData)
	if err != nil {
		return nil, err
	}

	return responseData.Users, nil
}

func (v *V2b) UpdateUsers(interval time.Duration) {
	fmt.Println("用户列表更新已启动")
	for {
		userList, err := v.getUserList(context.Background(), interval)
		if err != nil {
			fmt.Printf("get user list failed: %v\n", err)
			time.Sleep(time.Second * 15)
			continue
		}
		newUsersMap := make(map[string]struct{}, len(userList))
		for _, user := range userList {
			newUsersMap[user.UUID] = struct{}{}
		}

		v.lock.Lock()
		v.usersMap = newUsersMap
		v.lock.Unlock()

		time.Sleep(interval)
	}
}

func (v *V2b) Authenticate(id string) bool {
	v.lock.RLock()
	defer v.lock.RUnlock()

	if _, exists := v.usersMap[id]; exists {
		return true
	}

	return false
}

func (v *V2b) Log(id string, tx uint64, rx uint64) bool {
	v.lock.Lock()
	defer v.lock.Unlock()

	if _, ok := v.usersMap[id]; !ok {
		return false
	}

	entry, ok := v.statsMap[id]
	if !ok {
		entry = &trafficStatsEntry{}
		v.statsMap[id] = entry
	}
	entry.Tx += tx
	entry.Rx += rx

	return true
}

type TrafficPushRequest struct {
	Data map[string][2]uint64
}

// 定时提交用户流量情况
func (v *V2b) PushTrafficToV2boardInterval(interval time.Duration) {
	fmt.Println("用户流量情况监控已启动")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if err := v.pushTrafficToV2board(
			fmt.Sprintf(
				"%s?token=%s&node_id=%d&node_type=%s",
				v.config.API+"/api/v1/server/UniProxy/push",
				v.config.Token,
				v.config.NodeID,
				v.config.NodeType,
			),
		); err != nil {
			fmt.Printf("push traffic to v2board failed: %v\n", err)
		}
	}
}

// 向 v2board 提交用户流量使用情况
func (v *V2b) pushTrafficToV2board(url string) (err error) {
	v.lock.Lock()
	request := TrafficPushRequest{
		Data: make(map[string][2]uint64),
	}
	for id, stats := range v.statsMap {
		request.Data[id] = [2]uint64{stats.Tx, stats.Rx}
	}
	// 清空流量记录
	maps.Clear(v.statsMap)
	v.lock.Unlock()

	if len(request.Data) == 0 {
		return nil
	}

	defer func() {
		if err != nil {
			v.lock.Lock()
			defer v.lock.Unlock()
			for id, stats := range request.Data {
				entry, ok := v.statsMap[id]
				if !ok {
					entry = &trafficStatsEntry{}
					v.statsMap[id] = entry
				}
				entry.Tx += stats[0]
				entry.Rx += stats[1]
			}
		}
	}()

	// 将请求对象转换为JSON
	jsonData, err := json.Marshal(request.Data)
	if err != nil {
		return err
	}

	// 发起HTTP请求并提交数据
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 检查HTTP响应状态，处理错误等
	if resp.StatusCode != http.StatusOK {
		return errors.New("HTTP request failed with status code: " + resp.Status)
	}

	return nil
}
