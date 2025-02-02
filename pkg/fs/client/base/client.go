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

package base

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"paddleflow/pkg/common/http/api"
	"paddleflow/pkg/common/http/core"
)

const (
	defaultInterval = 120
	defaultUserName = "root"
	MaxLinks        = 1000
	defaultTimeOut  = 200
)

var Client *_Client

type _Client struct {
	Uuid       string
	FsID       string
	FsName     string
	UserName   string
	Token      string
	httpClient *core.PFClient
}

func NewClient(fsID string, c *core.PFClient, userName string, token string) (*_Client, error) {
	_client := _Client{
		Uuid:       uuid.NewString(),
		FsID:       fsID,
		UserName:   userName,
		httpClient: c,
		Token:      token,
	}
	Client = &_client
	return Client, nil
}

func (c *_Client) GetFSMeta() (FSMeta, error) {
	log.Debugf("Http CLient is %v", *c)
	params := api.FsParams{
		FsID:  c.FsID,
		Token: c.Token,
	}
	fsResponseMeta, err := api.FsRequest(params, c.httpClient)
	if err != nil {
		log.Errorf("fs request failed: %v", err)
		return FSMeta{}, err
	}
	log.Debugf("the resp is [%+v]", fsResponseMeta)
	fsMeta := FSMeta{
		ID:            fsResponseMeta.Id,
		Name:          fsResponseMeta.Name,
		UfsType:       fsResponseMeta.Type,
		ServerAddress: fsResponseMeta.ServerAddress,
		SubPath:       fsResponseMeta.SubPath,
		Properties:    fsResponseMeta.Properties,
	}
	return fsMeta, nil
}

func (c *_Client) GetLinks() (map[string]FSMeta, error) {
	log.Debugf("http CLient is %v", *c)
	params := api.LinksParams{
		FsID:  c.FsID,
		Token: c.Token,
	}
	result := make(map[string]FSMeta)

	linkResult, err := api.LinksRequest(params, c.httpClient)
	if err != nil {
		log.Errorf("links request failed: %v", err)
		return nil, err
	}
	linkList := linkResult.LinkList

	for _, link := range linkList {
		result[link.FsPath] = FSMeta{
			Name:          link.FsName,
			UfsType:       link.Type,
			ServerAddress: link.ServerAddress,
			SubPath:       link.SubPath,
			Properties:    link.Properties,
			// type: fs 表示是默认的后端存储；link 表示是外部存储
			Type: LinkType,
		}
	}
	return result, nil
}
