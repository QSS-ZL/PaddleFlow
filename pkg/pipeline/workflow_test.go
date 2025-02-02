/*
Copyright (c) 2021 PaddlePaddle Authors. All Rights Reserve.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pipeline

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"

	"paddleflow/pkg/apiserver/common"
	"paddleflow/pkg/apiserver/models"
	"paddleflow/pkg/common/database/db_fake"
	"paddleflow/pkg/common/schema"
)

var mockCbs = WorkflowCallbacks{
	UpdateRunCb: func(runID string, event interface{}) bool {
		fmt.Println("UpdateRunCb: ", event)
		return true
	},
	LogCacheCb: func(req schema.LogRunCacheRequest) (string, error) {
		return "cch-000027", nil
	},
	ListCacheCb: func(firstFp, fsID, step, yamlPath string) ([]models.RunCache, error) {
		return []models.RunCache{models.RunCache{RunID: "run-000027"}, models.RunCache{RunID: "run-000028"}}, nil
	},
}

// 测试NewBaseWorkflow, 只传yaml内容
func TestNewBaseWorkflowByOnlyRunYaml(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	if err := bwf.validate(); err != nil {
		t.Errorf("aha %s", bwf.Name)
	}
}

// 测试 BaseWorkflow
func TestNewBaseWorkflow(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	fmt.Println(wfs)
	bwf := BaseWorkflow{
		Name:   "test_workflow",
		Desc:   "test base workflow",
		Entry:  "main",
		Source: wfs,
	}
	if err := bwf.validate(); err != nil {
		t.Errorf("test validate failed: Name[%s]", bwf.Name)
	}
}

// 测试带环流程
func TestNewBaseWorkflowWithCircle(t *testing.T) {
	testCase := loadcase("./testcase/run.circle.yaml")
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	_, err := bwf.topologicalSort(bwf.Source.EntryPoints)
	assert.NotNil(t, err)
}

// 测试带环流程
func TestTopologicalSort_noCircle(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)

	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	fmt.Println(bwf.Source.EntryPoints)
	result, err := bwf.topologicalSort(bwf.Source.EntryPoints)
	assert.Nil(t, err)
	assert.Equal(t, "data_preprocess", result[0])
	assert.Equal(t, "main", result[1])
	assert.Equal(t, "validate", result[2])

	bwf = NewBaseWorkflow(wfs, "", "main", nil, nil)
	runSteps := bwf.getRunSteps()
	assert.Equal(t, 2, len(runSteps))
	result, err = bwf.topologicalSort(bwf.Source.EntryPoints)
	assert.Nil(t, err)
	assert.Equal(t, "data_preprocess", result[0])
	assert.Equal(t, "main", result[1])
}

// 测试运行 Workflow 成功
func TestCreateNewWorkflowRun_success(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")

	controller := gomock.NewController(t)
	mockJob := NewMockJob(controller)
	NewStep = func(name string, wfr *WorkflowRuntime, info *schema.WorkflowSourceStep) (*Step, error) {
		return &Step{
			job:  mockJob,
			info: info,
		}, nil
	}
	mockJob.EXPECT().Job().Return(BaseJob{Id: "mockJobID"}).AnyTimes()
	mockJob.EXPECT().Succeeded().Return(true).AnyTimes()

	wfs := parseWorkflowSource(testCase)
	fmt.Printf("\n %+v \n", wfs)
	wf, err := NewWorkflow(wfs, "", "", nil, nil, mockCbs)
	if err != nil {
		t.Errorf("aha %s", err.Error())
	}

	time.Sleep(time.Millisecond * 10)
	wf.runtime.steps["data_preprocess"].done = true
	wf.runtime.steps["main"].done = true
	wf.runtime.steps["validate"].done = true

	go wf.Start()

	// todo: remove event, receive event from job
	event1 := WorkflowEvent{Event: WfEventJobUpdate, Extra: map[string]interface{}{"event1": "step 1 data_process finished"}}
	wf.runtime.event <- event1
	time.Sleep(time.Millisecond * 10)

	assert.Equal(t, common.StatusRunSucceeded, wf.runtime.status)
}

// 测试运行 Workflow 失败
func TestCreateNewWorkflowRun_failed(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	controller := gomock.NewController(t)
	mockJob := NewMockJob(controller)
	NewStep = func(name string, wfr *WorkflowRuntime, info *schema.WorkflowSourceStep) (*Step, error) {
		return &Step{
			job:       mockJob,
			info:      info,
			submitted: true,
		}, nil
	}
	mockJob.EXPECT().Job().Return(BaseJob{Id: "mockJobID"}).AnyTimes()
	mockJob.EXPECT().Started().Return(true).AnyTimes()
	mockJob.EXPECT().NotEnded().Return(false).AnyTimes()
	mockJob.EXPECT().Failed().Return(true).AnyTimes()
	mockJob.EXPECT().Succeeded().Return(false).AnyTimes()
	mockJob.EXPECT().Cached().Return(false).AnyTimes()

	wf, err := NewWorkflow(wfs, "", "", nil, nil, mockCbs)
	if err != nil {
		t.Errorf("aha %s", wf.Name)
	}

	go wf.Start()

	time.Sleep(time.Millisecond * 10)

	// todo: remove event, receive event from job
	event1 := WorkflowEvent{Event: WfEventJobUpdate, Extra: map[string]interface{}{"event1": "step 1 data_process finished"}}
	wf.runtime.event <- event1
	time.Sleep(time.Millisecond * 100)

	assert.Equal(t, common.StatusRunFailed, wf.runtime.status)
}

// 测试停止 Workflow
func TestStopWorkflowRun(t *testing.T) {
	testCase := loadcase("../../example/pipeline/run.yaml")
	wfs := parseWorkflowSource(testCase)
	extraInfo := map[string]string{
		WfExtraInfoKeySource:   "./testcase/run.yaml",
		WfExtraInfoKeyFsID:     "mockFsID",
		WfExtraInfoKeyUserName: "mockUser",
		WfExtraInfoKeyFsName:   "mockFsname",
	}


	controller := gomock.NewController(t)
	mockJob := NewMockJob(controller)
	mockJob.EXPECT().Job().Return(BaseJob{Id: "mockJobID"}).AnyTimes()
	mockJob.EXPECT().Succeeded().Return(true).AnyTimes()
	mockJob.EXPECT().Validate().Return(nil).AnyTimes()

	NewStep = func(name string, wfr *WorkflowRuntime, info *schema.WorkflowSourceStep) (*Step, error) {
		return &Step{
			job:  mockJob,
			info: info,
		}, nil
	}

	mockJobTerminated := NewMockJob(controller)
	mockJobTerminated.EXPECT().Job().Return(BaseJob{Id: "mockJobTerminatedID"}).AnyTimes()
	mockJobTerminated.EXPECT().Succeeded().Return(false).AnyTimes()
	mockJobTerminated.EXPECT().Failed().Return(false).AnyTimes()
	mockJobTerminated.EXPECT().Terminated().Return(true).AnyTimes()
	mockJobTerminated.EXPECT().Validate().Return(nil).AnyTimes()

	wf, err := NewWorkflow(wfs, "", "", nil, extraInfo, mockCbs)
	assert.Nil(t, err)
	if err != nil {
		println(err)
	}

	time.Sleep(time.Millisecond * 10)
	wf.runtime.steps["preprocess"].done = true
	wf.runtime.steps["preprocess"].job = mockJob
	wf.runtime.steps["train"].done = true
	wf.runtime.steps["train"].job = mockJob
	wf.runtime.steps["validate"].done = true
	wf.runtime.steps["validate"].job = mockJobTerminated

	go wf.Start()
	time.Sleep(time.Millisecond * 10)

	wf.Stop()
	assert.Equal(t, common.StatusRunTerminating, wf.runtime.status)
	// todo: remove event, receive event from job
	event1 := WorkflowEvent{Event: WfEventJobUpdate, Extra: map[string]interface{}{"event1": "step 1 data_process finished"}}
	wf.runtime.event <- event1
	time.Sleep(time.Millisecond * 30)

	assert.Equal(t, common.StatusRunTerminated, wf.runtime.status)
}

// 测试Workflow
func TestNewWorkflowFromEntry(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	baseWorkflow := NewBaseWorkflow(wfs, "", "main", nil, nil)
	wf := &Workflow{
		BaseWorkflow: baseWorkflow,
		callbacks:    mockCbs,
	}
	wf.newWorkflowRuntime()

	assert.Equal(t, 2, len(wf.runtime.steps))
	_, ok := wf.runtime.steps["main"]
	assert.True(t, ok)
	_, ok1 := wf.runtime.steps["data_preprocess"]
	assert.True(t, ok1)
	_, ok2 := wf.runtime.steps["validate"]
	assert.False(t, ok2)
}

func TestValidateWorkflow_WrongParam(t *testing.T) {
	source := schema.WorkflowSource{}
	testCase := loadcase("./testcase/run.wrongparam.yaml")
	err := yaml.Unmarshal(testCase, &source)
	if err != nil {
		t.Errorf("%s", err)
	}
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	err = bwf.validate()
	assert.NotNil(t, err)

	// parameter 在 step [main] 中有错误，然而在将 entry 设置成 [data_preprocess] 后， validate 仍然返回成功
	bwf = NewBaseWorkflow(wfs, "", "data_preprocess", nil, nil)
	err = bwf.validate()
	assert.Nil(t, err)
}

func TestValidateWorkflow(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	err := bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, bwf.Source.EntryPoints["validate"].Parameters["refSystem"].(string), "{{ PF_RUN_ID }}")

	bwf.Source.EntryPoints["validate"].Parameters["refSystem"] = "{{ xxx }}"
	err = bwf.validate()
	assert.NotNil(t, err)

	bwf.Source.EntryPoints["validate"].Parameters["refSystem"] = "{{ PF_RUN_ID }}"
	err = bwf.validate()
	assert.Nil(t, err)

	// ref from downstream
	bwf.Source.EntryPoints["main"].Parameters["invalidRef"] = "{{ validate.refSystem }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "invalid parameters reference {{ validate.refSystem }} in step main")

	// ref from downstream
	bwf.Source.EntryPoints["main"].Parameters["invalidRef"] = "{{ .refSystem }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "unsupported SysParamName[refSystem] for param[{{ .refSystem }}]")

	// validate param name
	bwf.Source.EntryPoints["main"].Parameters["invalidRef"] = "111" // 把上面的，改成正确的
	bwf.Source.EntryPoints["main"].Parameters["invalid-name"] = "xxx"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "format of parameters[invalid-name] in step[main] incorrect, should be in [a-zA-z0-9_]")

	// validate param name
	delete(bwf.Source.EntryPoints["main"].Parameters, "invalid-name") // 把上面的，改成正确的
	bwf.Source.EntryPoints["main"].Env["invalid-name"] = "xxx"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "format of env[invalid-name] in step[main] incorrect, should be in [a-zA-z0-9_]")
}

func TestValidateWorkflow__DictParam(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)

	err := bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "dictparam", bwf.Source.EntryPoints["main"].Parameters["p3"])
	assert.Equal(t, 0.66, bwf.Source.EntryPoints["main"].Parameters["p4"])
	assert.Equal(t, "/path/to/anywhere", bwf.Source.EntryPoints["main"].Parameters["p5"])

	// 缺 default 值
	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "path", "default": ""}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "invalid value[] in dict param[name: dict, value: {Type:path Default:}]", err.Error())

	// validate dict param
	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"kkk": 0.32}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "type[] is not supported for dict param[name: dict, value: {Type: Default:<nil>}]")

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"kkk": "111"}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "type[] is not supported for dict param[name: dict, value: {Type: Default:<nil>}]")

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "kkk"}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "invalid value[<nil>] in dict param[name: dict, value: {Type:kkk Default:<nil>}]")

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "float"}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "invalid value[<nil>] in dict param[name: dict, value: {Type:float Default:<nil>}]")

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "float", "default": "kkk"}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, InvalidParamTypeError("kkk", "float").Error(), err.Error())

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "float", "default": 111}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, 111, bwf.Source.EntryPoints["main"].Parameters["dict"])

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "string", "default": "111"}
	err = bwf.validate()
	assert.Nil(t, err)

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "path", "default": "111"}
	err = bwf.validate()
	assert.Nil(t, err)

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "path", "default": "/111"}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "/111", bwf.Source.EntryPoints["main"].Parameters["dict"])

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "path", "default": "/111 / "}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, err.Error(), "invalid path value[/111 / ] in parameter[dict]")

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "path", "default": "/111-1/111_2"}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "/111-1/111_2", bwf.Source.EntryPoints["main"].Parameters["dict"])
	assert.Equal(t, "dictparam", bwf.Source.EntryPoints["main"].Parameters["p3"])
	assert.Equal(t, 0.66, bwf.Source.EntryPoints["main"].Parameters["p4"])
	assert.Equal(t, "/path/to/anywhere", bwf.Source.EntryPoints["main"].Parameters["p5"])

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "float", "default": 111}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, 111, bwf.Source.EntryPoints["main"].Parameters["dict"])

	// invalid actual interface type
	mapParam := map[string]string{"ffff": "2"}
	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": "float", "default": mapParam}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, InvalidParamTypeError(mapParam, "float").Error(), err.Error())

	bwf.Source.EntryPoints["main"].Parameters["dict"] = map[interface{}]interface{}{"type": map[string]string{"ffff": "2"}, "default": "111"}
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "invalid dict parameter[map[default:111 type:map[ffff:2]]]", err.Error())

	// unsupported type
	param := map[interface{}]interface{}{"type": "unsupportedType", "default": "111"}
	bwf.Source.EntryPoints["main"].Parameters["dict"] = param
	err = bwf.validate()
	assert.NotNil(t, err)
	dictParam := DictParam{}
	decodeErr := dictParam.From(param)
	assert.Nil(t, decodeErr)
	assert.Equal(t, UnsupportedDictParamTypeError("unsupportedType", "dict", dictParam).Error(), err.Error())
}

func TestValidateWorkflowArtifacts(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)

	err := bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "{{ data_preprocess.train_data }}", bwf.Source.EntryPoints["main"].Artifacts.Input["train_data"])

	// 上游 output artifact 不存在
	bwf.Source.EntryPoints["main"].Artifacts.Input["wrongdata"] = "{{ xxxx }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "unsupported RefParamName[xxxx] for param[{{ xxxx }}]", err.Error())

	// 上游 output artifact 不存在
	bwf.Source.EntryPoints["main"].Artifacts.Input["wrongdata"] = "{{ data_preprocess.moexist_data }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "invalid inputArtifacts reference {{ data_preprocess.moexist_data }} in step main", err.Error())
	bwf.Source.EntryPoints["main"].Artifacts.Input["wrongdata"] = "/path/to/xxx"

	// 上游 output artifact 不存在
	bwf.Source.EntryPoints["main"].Artifacts.Output["wrongdata"] = "{{ xxxx }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "unsupported RefParamName[xxxx] for param[{{ xxxx }}]", err.Error())

	// 上游 output artifact 不存在
	bwf.Source.EntryPoints["main"].Artifacts.Output["wrongdata"] = "{{ data_preprocess.train_data }}"
	err = bwf.validate()
	assert.NotNil(t, err)
	assert.Equal(t, "output artifact[{{ data_preprocess.train_data }}] cannot refer upstream artifact", err.Error())
}

func TestValidateWorkflowParam_success(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	// not exist in source yaml
	bwf := NewBaseWorkflow(wfs, "", "", nil, nil)
	bwf.Params = map[string]interface{}{
		"p1": "correct",
	}
	err := bwf.validate()
	assert.NotNil(t, err)

	bwf.Params = map[string]interface{}{
		"model": "correct",
	}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "correct", bwf.Source.EntryPoints["main"].Parameters["model"])

	bwf.Source.EntryPoints["main"].Parameters["model"] = "xxxxx"
	bwf.Params = map[string]interface{}{
		"main.model": "correct",
	}
	err = bwf.validate()
	assert.Nil(t, err)
	assert.Equal(t, "correct", bwf.Source.EntryPoints["main"].Parameters["model"])

	bwf.Params = map[string]interface{}{
		"model": "{{ PF_RUN_ID }}",
	}
	err = bwf.validate()
	assert.Nil(t, err)

	bwf.Params = map[string]interface{}{
		"model": "{{ xxx }}",
	}
	err = bwf.validate()
	assert.Equal(t, "unsupported SysParamName[xxx] for param[{{ xxx }}]", err.Error())

	bwf.Params = map[string]interface{}{
		"model": "{{ step1.param }}",
	}
	err = bwf.validate()
	assert.Equal(t, "invalid parameters reference {{ step1.param }} in step main", err.Error())
}

func TestRestartWorkflow(t *testing.T) {
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	controller := gomock.NewController(t)
	mockJob := NewMockJob(controller)
	NewStep = func(name string, wfr *WorkflowRuntime, info *schema.WorkflowSourceStep) (*Step, error) {
		return &Step{
			job:       mockJob,
			info:      info,
			submitted: true,
		}, nil
	}
	wf, err := NewWorkflow(wfs, "", "", nil, nil, mockCbs)
	if err != nil {
		t.Errorf("aha %s", wf.Name)
	}

	runtimeView := schema.RuntimeView{
		"data_preprocess": schema.JobView{
			JobID:  "data_preprocess",
			Status: schema.StatusJobSucceeded,
		},
		"main": {
			JobID:  "data_preprocess",
			Status: schema.StatusJobRunning,
		},
		"validate": {
			JobID: "",
		},
	}

	err = wf.SetWorkflowRuntime(runtimeView)
	assert.Nil(t, err)
	assert.Equal(t, true, wf.runtime.steps["data_preprocess"].done)
	assert.Equal(t, true, wf.runtime.steps["data_preprocess"].submitted)
	assert.Equal(t, false, wf.runtime.steps["main"].done)
	assert.Equal(t, true, wf.runtime.steps["main"].submitted)
	assert.Equal(t, false, wf.runtime.steps["validate"].done)
	assert.Equal(t, false, wf.runtime.steps["validate"].submitted)
}

func TestRestartWorkflow_from1completed(t *testing.T) {
	db_fake.InitFakeDB()
	testCase := loadcase("./testcase/run.yaml")
	wfs := parseWorkflowSource(testCase)
	controller := gomock.NewController(t)
	mockJob := NewMockJob(controller)
	NewStep = func(name string, wfr *WorkflowRuntime, info *schema.WorkflowSourceStep) (*Step, error) {
		return &Step{
			job:       mockJob,
			info:      info,
			submitted: true,
		}, nil
	}
	wf, err := NewWorkflow(wfs, "", "", nil, nil, mockCbs)
	if err != nil {
		t.Errorf("aha %s", err.Error())
	}

	runtimeView := schema.RuntimeView{
		"data_preprocess": schema.JobView{
			JobID:  "data_preprocess",
			Status: schema.StatusJobSucceeded,
		},
		"main": {
			JobID: "",
		},
		"validate": {
			JobID: "",
		},
	}
	err = wf.SetWorkflowRuntime(runtimeView)
	assert.Nil(t, err)
	assert.Equal(t, true, wf.runtime.steps["data_preprocess"].done)
	assert.Equal(t, true, wf.runtime.steps["data_preprocess"].submitted)
	assert.Equal(t, false, wf.runtime.steps["main"].done)
	assert.Equal(t, false, wf.runtime.steps["main"].submitted)
	assert.Equal(t, false, wf.runtime.steps["validate"].done)
	assert.Equal(t, false, wf.runtime.steps["validate"].submitted)
}
