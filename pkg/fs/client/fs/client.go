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

package fs

import (
	"io"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"paddleflow/pkg/fs/client/base"
)

var defaultPfsServer string
var linkMetaDirPrefix string

// pfsServer服务地址
func SetPFSServer(server string) {
	defaultPfsServer = server
}

func SetLinkMetaDirPrefix(dirPrefix string) {
	linkMetaDirPrefix = dirPrefix
}

var (
	MemCacheSize    = 1 << 26 // 64M
	MemCacheExpire  = 60 * time.Second
	DiskCacheSize   = 1 << 29 // 512M
	DiskCacheExpire = 5 * time.Minute
	BlockSize       = 1 << 22 // 4M
	DiskCachePath   = "./cache_dir"
)

func SetMemCache(size int, expire time.Duration) {
	MemCacheSize = size
	MemCacheExpire = expire
}

func SetDiskCache(path string, expire time.Duration) {
	DiskCacheExpire = expire
	DiskCachePath = path
}

func SetBlockSize(size int) {
	BlockSize = size
}

type FSClient interface {
	Create(path string) (io.WriteCloser, error)
	Open(path string) (io.ReadCloser, error)
	CreateFile(path string, content []byte) (int, error)
	SaveFile(file io.Reader, destPath, fileName string) error
	Remove(path string) error
	RemoveAll(path string) error
	IsDir(path string) (bool, error)
	Exist(path string) (bool, error)
	IsEmptyDir(path string) (bool, error)
	Mkdir(path string, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	ListDir(path string) ([]os.FileInfo, error)
	Readdirnames(path string, n int) ([]string, error)
	Rename(srcPath, dstPath string) error
	Copy(srcPath, dstPath string) error
	Size(path string) (int64, error)
	Chmod(path string, fm os.FileMode) error
	Walk(root string, walkFn filepath.WalkFunc) error
	Stat(path string) (os.FileInfo, error)
}

func NewFSClientWithServer(server string, fsID string) (FSClient, error) {
	return newFSClient(server, fsID)
}

func NewFSClientWithFsID(fsID string) (FSClient, error) {
	return newFSClient(defaultPfsServer, fsID)
}

func NewFSClient(fsMeta base.FSMeta, links map[string]base.FSMeta) (FSClient, error) {
	return newFSClientWithFsMeta(fsMeta, links, "")
}

func newFSClient(server, fsID string) (FSClient, error) {
	fsMeta, links, err := getMetaAndLinks(server, fsID)
	if err != nil {
		log.Errorf("get fs meta and links failed: %v", err)
		return nil, err
	}
	return newFSClientWithFsMeta(fsMeta, links, server)
}

func newFSClientWithFsMeta(fsMeta base.FSMeta, links map[string]base.FSMeta, server string) (FSClient, error) {
	if fsMeta.UfsType == base.MockType {
		return &MockClient{pathPrefix: fsMeta.SubPath}, nil
	}
	client, err := NewPFSClient(fsMeta, links)
	client.fsID = fsMeta.ID
	client.server = server
	return client, err
}
