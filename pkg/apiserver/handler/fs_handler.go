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

package handler

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	"paddleflow/pkg/common/config"
	"paddleflow/pkg/fs/client/base"
	"paddleflow/pkg/fs/client/fs"
	"paddleflow/pkg/fs/server/api/request"
	"paddleflow/pkg/fs/server/service"
)

const (
	maxRetryCount = 3
	// unit is time.Millisecond
	sleepMillisecond = 100
)

var defaultFsServer string
var defaultFsHost string
var defaultFsPort int

type FsServerEmptyError struct {
}

func (e FsServerEmptyError) Error() string {
	return fmt.Sprint("the server of fs is empty, please set the value of it")
}

var ReadFileFromFs = func (fsID, filePath string, logEntry *log.Entry) ([]byte, error) {
	fsHandle, err := NewFsHandlerWithServer(fsID, config.GlobalServerConfig.ApiServer.Host, config.GlobalServerConfig.ApiServer.Port, logEntry)
	if err != nil {
		logEntry.Errorf("NewFsHandler failed. err: %v", err)
		return nil, err
	}
	runYaml, err := fsHandle.ReadFsFile(filePath)
	if err != nil {
		logEntry.Errorf("NewFsHandler failed. err: %v", err)
		return nil, err
	}
	return runYaml, nil
}

type FsHandler struct {
	log      *log.Entry
	fsID     string
	fsClient fs.FSClient
}

func SetFsServer(host string, port int) {
	fsServicePath := fmt.Sprintf("%s:%d", host, port)
	defaultFsServer = fsServicePath
	defaultFsHost = host
	defaultFsPort = port
	fs.SetPFSServer(fsServicePath)
}

func NewFsHandler(fsID string, logEntry *log.Entry) (*FsHandler, error) {
	logEntry.Debugf("begin to new a FsHandler with defaultFsServer[%s]", defaultFsServer)
	if defaultFsServer == "" {
		err := FsServerEmptyError{}
		return nil, err
	}
	return NewFsHandlerWithServer(fsID, defaultFsHost, defaultFsPort, logEntry)
}

// 方便单测
var NewFsHandlerWithServer = func(fsID, fsHost string, fsPort int, logEntry *log.Entry) (*FsHandler, error) {
	logEntry.Debugf("begin to new a FsHandler with fsID[%s] and server host[%s], rpcPort[%d]",
		fsID, fsHost, fsPort)
	var fsClientError error = nil
	var fsHandler FsHandler
	var fsClient fs.FSClient

	for i := 0; i < maxRetryCount; i++ {
		fsClientError = nil
		fsHandler = FsHandler{
			fsID: fsID,
		}
		fsClient, fsClientError = fsHandler.getFSClient()
		if fsClientError != nil {
			logEntry.Errorf("new a FSClient with fsID[%s] failed: %v", fsID, fsClientError)
			time.Sleep(sleepMillisecond * time.Millisecond)
			continue
		}
		fsHandler.fsClient = fsClient
		fsHandler.log = logEntry
		break
	}
	if fsClientError != nil {
		return nil, fsClientError
	}
	return &fsHandler, nil
}

// 方便其余模块调用 fsHandler单测
func MockerNewFsHandlerWithServer(fsID, fsHost string, fsRpcPort int, logEntry *log.Entry) (*FsHandler, error) {
	os.MkdirAll("./mock_fs_handler", 0755)

	testFsMeta := base.FSMeta{
		UfsType: base.LocalType,
		SubPath: "./mock_fs_handler",
	}

	fsClient, err := fs.NewFSClientForTest(testFsMeta)
	if err != nil {
		return nil, err
	}

	fsHandler := FsHandler{
		fsClient: fsClient,
		log:      logEntry,
	}

	return &fsHandler, nil
}

func (fh *FsHandler) ReadFsFile(path string) ([]byte, error) {
	fh.log.Debugf("begin to get the content of file[%s] for fsId[%s]",
		path, fh.fsID)

	Reader, err := fh.fsClient.Open(path)
	if err != nil {
		fh.log.Errorf("Read the content of file[%s] for fsID [%s] failed: %s",
			path, fh.fsID, err.Error())
		return nil, err
	}
	defer Reader.Close()

	content, err := ioutil.ReadAll(Reader)
	if err != nil {
		fh.log.Errorf("Read the content of file[%s] for fsID [%s] failed: %s",
			path, fh.fsID, err.Error())
		return nil, err
	}

	return content, nil
}

func (fh *FsHandler) Stat(path string) (os.FileInfo, error) {
	fh.log.Debugf("begin to get the stat of file[%s] with fsId[%s]",
		path, fh.fsID)

	fileInfo, err := fh.fsClient.Stat(path)
	if err != nil {
		fh.log.Errorf("get the stat of file[%s] with fsID [%s] failed: %s",
			path, fh.fsID, err.Error())
		return fileInfo, err
	}

	return fileInfo, err
}

func (fh *FsHandler) ModTime(path string) (time.Time, error) {
	fh.log.Debugf("begin to get the modtime of file[%s] with fsId[%s]",
		path, fh.fsID)

	fileInfo, err := fh.Stat(path)
	if err != nil {
		fh.log.Debugf("get the modtime of file[%s] with fsId[%s] failed: %s",
			path, fh.fsID, err.Error())
		return time.Time{}, err
	}

	modTime := fileInfo.ModTime()
	return modTime, nil
}

func (fh *FsHandler) getFSClient() (fs.FSClient, error) {
	fsService := service.GetFileSystemService()
	fsModel, err := fsService.GetFileSystem(&request.GetFileSystemRequest{}, fh.fsID)
	if err != nil {
		log.Errorf("get file system with fsID[%s] error[%v]", fh.fsID, err)
		return nil, err
	}

	linkService := service.GetLinkService()
	listLinks, _, err := linkService.GetLink(&request.GetLinkRequest{FsID: fh.fsID})
	if err != nil {
		log.Errorf("get file system links with fsID[%s] error[%v]", fh.fsID, err)
		return nil, err
	}

	fsMeta := base.FSMeta{
		ID:            fsModel.ID,
		Name:          fsModel.Name,
		UfsType:       fsModel.Type,
		ServerAddress: fsModel.ServerAddress,
		SubPath:       fsModel.SubPath,
		Properties:    fsModel.PropertiesMap,
		Type:          base.FSType,
	}
	links := make(map[string]base.FSMeta)
	for _, link := range listLinks {
		links[link.FsPath] = base.FSMeta{
			ID:            link.ID,
			UfsType:       link.Type,
			ServerAddress: link.ServerAddress,
			SubPath:       link.SubPath,
			Properties:    link.PropertiesMap,
			Type:          base.LinkType,
		}
	}
	return fs.NewFSClient(fsMeta, links)
}
