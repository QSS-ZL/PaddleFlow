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
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi"
	log "github.com/sirupsen/logrus"

	apicommon "paddleflow/pkg/apiserver/common"
	"paddleflow/pkg/apiserver/models"
	"paddleflow/pkg/apiserver/router/util"
	"paddleflow/pkg/common/config"
	"paddleflow/pkg/common/logger"
	"paddleflow/pkg/fs/client/base"
	fuse "paddleflow/pkg/fs/client/fs"
	"paddleflow/pkg/fs/server/api/common"
	"paddleflow/pkg/fs/server/api/request"
	"paddleflow/pkg/fs/server/api/response"
	"paddleflow/pkg/fs/server/service"
	"paddleflow/pkg/fs/server/utils/fs"
)

type LinkRouter struct{}

func (lr *LinkRouter) Name() string {
	return "LinkRouter"
}

func (lr *LinkRouter) AddRouter(r chi.Router) {
	log.Info("add fsLink router")
	r.Post("/link", lr.CreateLink)
	r.Delete("/link/{fsName}", lr.DeleteLink)
	r.Get("/link/{fsName}", lr.GetLink)

}

var SupportLinkURLPrefix = map[string]bool{
	fs.HDFS:  true,
	fs.Local: true,
	fs.S3:    true,
	fs.SFTP:  true,
}

// CreateLink the function that handle the create Link request
// @Summary CreateLink
// @Description 创建文件系统的link
// @tag fs
// @Accept   json
// @Produce  json
// @Param request body request.CreateLinkRequest true "request body"
// @Success 200 {object} models.Link
// @Failure 400 {object} common.ErrorResponse
// @Failure 404 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @Router /api/paddleflow/v1/link/ [post]
func (lr *LinkRouter) CreateLink(w http.ResponseWriter, r *http.Request) {
	ctx := common.GetRequestContext(r)

	var linkRequest request.CreateLinkRequest
	err := common.BindJSON(r, &linkRequest)
	if err != nil {
		ctx.Logging().Errorf("CreateLink bindjson failed. err:%s", err.Error())
		common.RenderErr(w, ctx.RequestID, common.MalformedJSON)
		return
	}
	log.Debugf("create link with req[%v]", linkRequest)

	linkService := service.GetLinkService()
	if linkRequest.Username == "" {
		linkRequest.Username = ctx.UserName
	}
	err = validateCreateLink(&ctx, &linkRequest)
	if err != nil {
		ctx.Logging().Errorf(
			"create link params error: %v", err.Error())
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
		return
	}

	linkModel, err := linkService.CreateLink(&ctx, &linkRequest)
	if err != nil {
		ctx.Logging().Errorf("create link with error[%v]", err)
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
		return
	}

	if errPersist := linkService.PersistLinksMeta(linkModel.FsID); errPersist != nil {
		ctx.Logging().Errorf("persist links meta with err[%v]", errPersist)
		ctx.ErrorCode = common.LinkMetaPersistError
		err := models.DeleteLinkWithFsIDAndFsPath(fs.ID(linkRequest.Username, linkRequest.FsName), linkRequest.FsPath)
		if err != nil {
			ctx.Logging().Errorf("delete link err with fsID[%s] and fsPath[%s]", fs.ID(linkRequest.Username, linkRequest.FsName), linkRequest.FsPath)
			ctx.ErrorCode = common.LinkModelError
			common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
			return
		}
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, errPersist.Error())
		return
	}

	common.Render(w, http.StatusOK, nil)
}

func validateCreateLink(ctx *logger.RequestContext, req *request.CreateLinkRequest) error {
	if req.Username == "" {
		ctx.Logging().Error("userName is empty")
		ctx.ErrorCode = common.AuthFailed
		return fmt.Errorf("userName is empty")
	}

	urlArr := strings.Split(req.Url, ":")
	if len(urlArr) < 2 {
		ctx.Logging().Errorf("[%s] is not a correct link file-system url", req.Url)
		ctx.ErrorCode = common.InvalidLinkURL
		return common.InvalidField("url", "is not a correct link file-system url")
	}

	fileSystemType := urlArr[0]
	fsID := fs.ID(req.Username, req.FsName)

	if !SupportLinkURLPrefix[fileSystemType] {
		ctx.Logging().Errorf("url[%s] can not support [%s] file system", req.Url, fileSystemType)
		ctx.ErrorCode = common.InvalidLinkURL
		return common.InvalidField("url", fmt.Sprintf("link can not support [%s] file system", fileSystemType))
	}

	err := checkLinkURLFormat(fileSystemType, req.Url, req.Properties)
	if err != nil {
		ctx.Logging().Errorf("check url format err[%v] with url[%s]", err, req.Url)
		ctx.ErrorCode = common.InvalidLinkURL
		return err
	}

	err = checkLinkProperties(fileSystemType, req)
	if err != nil {
		ctx.Logging().Errorf("check link properties err[%v] with properites[%v]", err, req.Properties)
		ctx.ErrorCode = common.InvalidLinkProperties
		return err
	}

	err = checkLinkPath(req.FsPath, fs.ID(req.Username, req.FsName))
	if err != nil {
		ctx.Logging().Errorf("check fs dir err[%v] with path[%s]", err, req.FsPath)
		ctx.ErrorCode = common.InvalidLinkURL
		return err
	}

	fileSystemModel, err := models.GetFileSystemWithFsID(fsID)
	if err != nil {
		ctx.Logging().Errorf("GetFileSystemWithFsID error[%v]", err)
		ctx.ErrorCode = common.LinkModelError
		return err
	}
	if fileSystemModel.ID == "" {
		ctx.Logging().Errorf("link with fsID[%s] is not exist", fsID)
		ctx.ErrorCode = common.LinkFileSystemNotExist
		return common.InvalidField("fsName", fmt.Sprintf("user[%s] fsName[%s] is not exist", req.Username, req.FsName))
	}
	// local的文件系统不支持link其他文件系统，其他文件系统支持link local类型的文件系统
	if !SupportLinkURLPrefix[fileSystemModel.Type] || fileSystemModel.Type == base.LocalType {
		ctx.Logging().Errorf("fs name[%s] type[%s] can not support link feature", req.FsName, fileSystemModel.Type)
		ctx.ErrorCode = common.InvalidFileSystemFsName
		return common.InvalidField("fsName", fmt.Sprintf("fs name[%s] type[%s] can not support link feature", req.FsName, fileSystemModel.Type))
	}

	// check fsName with fsPath is exist
	linkModel, err := models.LinkWithFsIDAndFsPath(fsID, req.FsPath)
	if err != nil {
		ctx.Logging().Errorf("create link failed error[%v]", err)
		ctx.ErrorCode = common.FileSystemDataBaseError
		return err
	}
	if linkModel.ID != "" {
		ctx.Logging().Errorf("link is exit linkID[%s]", linkModel.ID)
		ctx.ErrorCode = common.FileSystemNotExist
		return common.InvalidField("fsName fsPath", fmt.Sprintf("fsName[%s] has link path[%s]", req.FsName, req.FsPath))
	}

	fsMeta := base.FSMeta{
		ID:            fsID,
		Name:          req.FsName,
		UfsType:       fileSystemModel.Type,
		ServerAddress: fileSystemModel.ServerAddress,
		SubPath:       fileSystemModel.SubPath,
		Properties:    fileSystemModel.PropertiesMap,
		Type:          base.FSType,
	}

	err = checkFsPathIsExist(ctx, fsMeta, req.FsPath)
	if err != nil {
		ctx.Logging().Errorf("fsPath[%s] is not exist with err[%v]", req.FsPath, err)
		return err
	}

	fsType, serverAddress, subPath := fs.InformationFromURL(req.Url, req.Properties)
	fsLinkMeta := base.FSMeta{
		ID:            fsID,
		Name:          req.FsName,
		UfsType:       fsType,
		ServerAddress: serverAddress,
		SubPath:       subPath,
		Properties:    req.Properties,
		Type:          base.FSType,
	}
	err = checkStorageConnectivity(fsLinkMeta)
	if err != nil {
		ctx.Logging().Errorf("check fs[%s] connectivity failed with req[%v] and err[%v]", fileSystemModel.Name, req, err)
		ctx.ErrorCode = common.ConnectivityFailed
		return err
	}

	return nil
}

func checkFsPathIsExist(ctx *logger.RequestContext, fsMeta base.FSMeta, fsPath string) error {
	client, err := fuse.NewFSClient(fsMeta, nil)
	if err != nil {
		ctx.Logging().Errorf("fuse client err[%v]", err)
		ctx.ErrorCode = common.FuseClientError
		return err
	}
	isDir, err := client.IsDir(filepath.Dir(fsPath))
	if err != nil {
		ctx.Logging().Errorf("fuse client path[%s] exist err[%v]", fsPath, err)
		ctx.ErrorCode = common.FuseClientError
		return err
	}
	if !isDir {
		ctx.Logging().Errorf("file system[%s] has not path[%s]", fsMeta.ID, fsPath)
		ctx.ErrorCode = common.LinkFileSystemPathNotExist
		return common.InvalidField("fsPath", fmt.Sprintf("file system[%s] has not path[%s]", fsMeta.Name, fsPath))
	}

	isEmpty, err := client.IsEmptyDir(fsPath)
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			return nil
		}
		ctx.Logging().Errorf("fuse client path[%s] list err[%v]", fsPath, err)
		ctx.ErrorCode = common.FuseClientError
		return err
	}
	if !isEmpty {
		ctx.Logging().Errorf("path[%s] must be empty", fsPath)
		ctx.ErrorCode = common.LinkPathMustBeEmpty
		return common.InvalidField("fsPath", fmt.Sprintf("fspath[%s] directory must be empty", fsPath))
	}
	return nil
}

func checkLinkURLFormat(fsType, url string, properties map[string]string) error {
	urlSplit := strings.Split(url, "/")
	switch fsType {
	case fs.HDFS, fs.SFTP:
		if len(urlSplit) < 4 {
			log.Errorf("%s url split error", fsType)
			return common.InvalidField("url", fmt.Sprintf("%s url format is wrong", fsType))
		}
	case fs.Local:
		if len(urlSplit) < 3 {
			log.Errorf("%s url split error", fsType)
			return common.InvalidField("url", fmt.Sprintf("%s address format is wrong", fsType))
		}
		if urlSplit[2] == "" || urlSplit[2] == "root" {
			log.Errorf("%s path can not be empty or use root path", fsType)
			return common.InvalidField("url", fmt.Sprintf("%s path can not be empty or use root path", fsType))
		}
	case fs.S3:
		if len(urlSplit) < fs.S3SplitLen {
			log.Errorf("%s url split error", fsType)
			return common.InvalidField("url", fmt.Sprintf("%s url format is wrong", fsType))
		}
		if urlSplit[fs.S3EndpointSplit] == "" {
			log.Errorf("%s path must appoint bucket", fsType)
			return common.InvalidField("url", fmt.Sprintf("%s must appoint bucket", fsType))
		}
		if properties == nil {
			log.Error("properties is empty")
			return common.InvalidField("properties", "must not be empty")
		}
		properties[base.Bucket] = urlSplit[fs.S3EndpointSplit]
	}
	return nil
}

func checkLinkProperties(fsType string, req *request.CreateLinkRequest) error {
	switch fsType {
	case fs.HDFS:
		if req.Properties[base.KeyTabData] != "" {
			err := fs.CheckKerberosProperties(req.Properties)
			if err != nil {
				log.Errorf("check kerberos properties err[%v]", err)
				return err
			}
		} else if req.Properties[base.UserKey] != "" {
			if req.Properties[base.UserKey] == "" {
				return common.InvalidField("properties", "key[user] cannot be empty")
			}
			if req.Properties[base.Group] == "" {
				return common.InvalidField("properties", "key[group] cannot be empty")
			}
		} else {
			return common.InvalidField("properties", "not correct hdfs properties")
		}
		return nil
	case fs.Local:
		if req.Properties["debug"] != "true" {
			return common.InvalidField("debug", "properties key[debug] must true")
		}
		return nil
	case fs.S3:
		if req.Properties[base.AccessKey] == "" || req.Properties[base.SecretKey] == "" {
			log.Error("s3 ak or sk is empty")
			return common.InvalidField("properties", fmt.Sprintf("key %s or %s is empty", base.AccessKey, base.SecretKey))
		}
		if req.Properties[base.Endpoint] == "" {
			log.Error("endpoint is empty")
			return common.InvalidField("properties", "key[endpoint] is empty")
		}
		if req.Properties[base.Bucket] == "" {
			log.Error("bucket is empty")
			return common.InvalidField("properties", "url bucket is empty")
		}
		if req.Properties[base.Region] == "" {
			req.Properties[base.Region] = ""
		}
		encodedSk, err := apicommon.AesEncrypt(req.Properties[base.SecretKey], apicommon.AESEncryptKey)
		if err != nil {
			log.Errorf("encrypt s3 sk failed: %v", err)
			return err
		}
		req.Properties[base.SecretKey] = encodedSk
		return nil
	case fs.SFTP:
		if req.Properties[base.UserKey] == "" {
			return common.InvalidField(base.UserKey, "key[user] cannot be empty")
		}
		if req.Properties[base.Password] == "" {
			return common.InvalidField("properties", "key[password] cannot be empty")
		}
		encodePassword, err := apicommon.AesEncrypt(req.Properties[base.Password], apicommon.AESEncryptKey)
		if err != nil {
			log.Errorf("encrypt sftp password failed: %v", err)
			return err
		}
		req.Properties[base.Password] = encodePassword
		return nil
	default:
		return nil
	}
}

// checkLinkPath duplicate and nesting of the same storage link directory is not supported
func checkLinkPath(fsPath, fsID string) error {
	linkList, err := models.FsNameLinks(fsID)
	if err != nil {
		return err
	}
	for _, link := range linkList {
		if fs.CheckFsNested(fsPath, link.FsPath) {
			log.Errorf("%s and %s subpath is not allowed up nesting or duplication", fsPath, link.FsPath)
			return common.LinkPathError(fsPath)
		}
	}
	return nil
}

// DeleteLink the function that handle the delete file system link request
// @Summary DeleteLink
// @Description 删除指定文件系统的link
// @tag fs
// @Accept   json
// @Produce  json
// @Param fsName path string true "文件系统名称"
// @Param fsPath path string true "文件系统link的目录"
// @Success 200
// @Router /api/paddleflow/v1/link/{fsName} [delete]
func (lr *LinkRouter) DeleteLink(w http.ResponseWriter, r *http.Request) {
	ctx := common.GetRequestContext(r)
	fsName := chi.URLParam(r, util.QueryFsName)
	deleteRequest := &request.DeleteLinkRequest{
		FsName:   fsName,
		FsPath:   r.URL.Query().Get(util.QueryFsPath),
		Username: r.URL.Query().Get(util.QueryKeyUserName),
	}
	log.Debugf("delete link with req[%v]", deleteRequest)

	linkService := service.GetLinkService()

	err := validateDeleteLink(&ctx, deleteRequest)
	if err != nil {
		ctx.Logging().Errorf("validateDeleteLink error[%v]", err)
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
		return
	}

	err = linkService.DeleteLink(&ctx, deleteRequest)
	if err != nil {
		ctx.Logging().Errorf("delete link with error[%v]", err)
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
		return
	}

	fsID := fs.ID(deleteRequest.Username, deleteRequest.FsName)
	if errPersist := linkService.PersistLinksMeta(fsID); errPersist != nil {
		log.Errorf("persist links meta with err[%v]", errPersist)
	}
	common.Render(w, http.StatusOK, nil)
}

func validateDeleteLink(ctx *logger.RequestContext, req *request.DeleteLinkRequest) error {
	if req.Username == "" {
		req.Username = ctx.UserName
	}

	if req.Username == "" {
		ctx.Logging().Error("UserName is empty")
		ctx.ErrorCode = common.AuthFailed
		return common.InvalidField("userName", "userName is empty")
	}

	fsID := fs.ID(req.Username, req.FsName)
	link, err := models.LinkWithFsIDAndFsPath(fsID, req.FsPath)
	if err != nil {
		ctx.Logging().Errorf("link with fsID and fsPath error: %v", err)
		ctx.ErrorCode = common.LinkModelError
		return err
	}

	if link.FsID == "" {
		ctx.Logging().Errorf("link is not exit with %s and %s", fsID, req.FsPath)
		ctx.ErrorCode = common.LinkModelError
		return common.InvalidField("fsPath", fmt.Sprintf("user[%s] fsname[%s] not created fspath[%s]", req.Username, req.FsName, req.FsPath))
	}
	return nil
}

// GetLink the function that handle the list file system links request
// @Summary GetLink
// @Description 批量获取某个文件系统的link，root用户可以获取所有的link
// @tag fs
// @Accept   json
// @Produce  json
// @Param request body request.GetLinkRequest true "request body"
// @Success 200 {object} response.GetLinkResponse
// @Failure 400 {object} common.ErrorResponse
// @Failure 404 {object} common.ErrorResponse
// @Failure 500 {object} common.ErrorResponse
// @Router /api/paddleflow/v1/link/{fsName} [get]
func (lr *LinkRouter) GetLink(w http.ResponseWriter, r *http.Request) {
	ctx := common.GetRequestContext(r)

	var maxKeys int
	if r.URL.Query().Get(util.QueryKeyMaxKeys) == "" {
		maxKeys = util.DefaultMaxKeys
	} else {
		maxKeys, _ = strconv.Atoi(r.URL.Query().Get(util.QueryKeyMaxKeys))
	}
	getRequest := &request.GetLinkRequest{
		FsID:     chi.URLParam(r, util.QueryFsName),
		Marker:   r.URL.Query().Get(util.QueryKeyMarker),
		MaxKeys:  int32(maxKeys),
		Username: r.URL.Query().Get(util.QueryKeyUserName),
		FsPath:   r.URL.Query().Get(util.QueryFsPath),
	}

	log.Debugf("list file system link with req[%v]", getRequest)

	linkService := service.GetLinkService()

	if getRequest.Username == "" {
		getRequest.Username = ctx.UserName
	}
	if getRequest.Username == "" {
		ctx.Logging().Error("userName is empty")
		common.RenderErrWithMessage(w, ctx.RequestID, common.AuthFailed, "userName is empty")
		return
	}

	if getRequest.MaxKeys == 0 {
		getRequest.MaxKeys = DefaultMaxKeys
	}
	if getRequest.MaxKeys > MaxAllowKeys {
		ctx.Logging().Error("too many max keys")
		common.RenderErrWithMessage(w, ctx.RequestID, common.InvalidFileSystemMaxKeys, fmt.Sprintf("maxKeys limit %d", MaxAllowKeys))
		return
	}
	// trans fsName to real fsID, for user they only use fsName，grpc client may be use fsID
	getRequest.FsID = fs.NameToFsID(getRequest.FsID, getRequest.Username)

	listLinks, nextMarker, err := linkService.GetLink(getRequest)
	if err != nil {
		ctx.Logging().Errorf("list link with error[%v]", err)
		common.RenderErrWithMessage(w, ctx.RequestID, ctx.ErrorCode, err.Error())
		return
	}

	response := *getLinkListResult(listLinks, nextMarker, getRequest.Marker)
	ctx.Logging().Debugf("GetLink Link:%v", string(config.PrettyFormat(response)))
	common.Render(w, http.StatusOK, response)
}

func linkResponseFromModel(link models.Link) *response.LinkResponse {
	fsName := fs.FSIDToName(link.FsID)
	return &response.LinkResponse{
		FsName:        fsName,
		FsPath:        link.FsPath,
		ServerAddress: link.ServerAddress,
		Type:          link.Type,
		SubPath:       link.SubPath,
		Username:      link.UserName,
		Properties:    link.PropertiesMap,
	}
}

func getLinkListResult(linkModel []models.Link, nextMarker, marker string) *response.GetLinkResponse {
	var linkLists []*response.LinkResponse
	for _, link := range linkModel {
		linkList := linkResponseFromModel(link)
		linkLists = append(linkLists, linkList)
	}
	ListFsResponse := &response.GetLinkResponse{
		Marker:    marker,
		LinkList:  linkLists,
		Truncated: false,
	}
	if nextMarker != "" {
		ListFsResponse.Truncated = true
		ListFsResponse.NextMarker = nextMarker
	}
	return ListFsResponse
}
