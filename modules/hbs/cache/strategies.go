// Copyright 2017 Xiaomi, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cache

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/toolkits/container/set"

	"github.com/open-falcon/falcon-plus/common/model"
	"github.com/open-falcon/falcon-plus/modules/hbs/db"
	"github.com/open-falcon/falcon-plus/modules/hbs/g"
)

type SafeStrategies struct {
	sync.RWMutex
	M map[int]*model.Strategy
}

var Strategies = &SafeStrategies{M: make(map[int]*model.Strategy)}

func (this *SafeStrategies) GetMap() map[int]*model.Strategy {
	this.RLock()
	defer this.RUnlock()
	return this.M
}

func (this *SafeStrategies) Init(tpls map[int]*model.Template) {
	m, err := db.QueryStrategies(tpls)
	if err != nil {
		return
	}

	this.Lock()
	defer this.Unlock()
	this.M = m
}

func GetBuiltinMetrics(hostname string) ([]*model.BuiltinMetric, error) {
	ret := []*model.BuiltinMetric{}
	hid, exists := HostMap.GetID(hostname)
	if !exists {
		return ret, nil
	}

	gids, exists := HostGroupsMap.GetGroupIds(hid)
	if !exists {
		return ret, nil
	}

	// 根据gids，获取绑定的所有tids
	// 这里把绑定到host group上的模板获取出来汇总监控项
	tidSet := set.NewIntSet()
	for _, gid := range gids {
		tids, exists := GroupTemplates.GetTemplateIds(gid)
		if !exists {
			continue
		}

		for _, tid := range tids {
			tidSet.Add(tid)
		}
	}

	tidSlice := tidSet.ToSlice()
	if len(tidSlice) == 0 {
		return ret, nil
	}

	// 继续寻找这些tid的ParentId
	allTpls := TemplateCache.GetMap()
	for _, tid := range tidSlice {
		pids := ParentIds(allTpls, tid)
		for _, pid := range pids {
			tidSet.Add(pid)
		}
	}

	// 终于得到了最终的tid列表
	tidSlice = tidSet.ToSlice()

	// 把tid列表用逗号拼接在一起
	count := len(tidSlice)
	tidStrArr := make([]string, count)
	for i := 0; i < count; i++ {
		tidStrArr[i] = strconv.Itoa(tidSlice[i])
	}

	metricsFromDB, err := db.QueryBuiltinMetrics(strings.Join(tidStrArr, ","))
	if err != nil {
		return nil, err
	}

	metricsFromAppTree, err := QueryMetricFromAppTree(hostname)
	if err != nil {
		return nil, err
	}
	var metrics []*model.BuiltinMetric
	// TODO metrics去重
	metrics = append(metrics, metricsFromDB...)
	metrics = append(metrics, metricsFromAppTree...)
	return metrics, nil
}

func ParentIds(allTpls map[int]*model.Template, tid int) (ret []int) {
	depth := 0
	for {
		if tid <= 0 {
			break
		}

		if t, exists := allTpls[tid]; exists {
			ret = append(ret, tid)
			tid = t.ParentId
		} else {
			break
		}

		depth++
		if depth == 10 {
			log.Println("[ERROR] template inherit cycle. id:", tid)
			return []int{}
		}
	}

	sz := len(ret)
	if sz <= 1 {
		return
	}

	desc := make([]int, sz)
	for i, item := range ret {
		j := sz - i - 1
		desc[j] = item
	}

	return desc
}

// QueryMetricFromAppTree
// 服务树接口获取监控项, 夜莺把架构简化了, 没有hbs, 直接从api获取监控项, 也是在服务树节点上绑定监控模板
// 目前为止所有的服务树设计, 一个主机只能绑定到一个服务树节点上。
func QueryMetricFromAppTree(hostname string) ([]*model.BuiltinMetric, error) {
	addr := g.Config().AppTree
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/tree/metrics?hostname=%s", addr, hostname))
	if err != nil {
		return nil, err
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var metrics []*model.BuiltinMetric
	err = json.Unmarshal(b, &metrics)
	if err != nil {
		return nil, err
	}
	return metrics, nil
}
