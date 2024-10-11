/*
Copyright (c) 2017-2023 VMware, Inc. All Rights Reserved.

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

package simulator

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

type Datastore struct {
	mo.Datastore
}

func (ds *Datastore) eventArgument() *types.DatastoreEventArgument {
	return &types.DatastoreEventArgument{
		Datastore:           ds.Self,
		EntityEventArgument: types.EntityEventArgument{Name: ds.Name},
	}
}

func (ds *Datastore) model(m *Model) error {
	info := ds.Info.GetDatastoreInfo()
	u, _ := url.Parse(info.Url)
	if u.Scheme == "ds" {
		// rewrite saved vmfs path to a local temp dir
		u.Path = path.Clean(u.Path)
		parent := strings.ReplaceAll(path.Dir(u.Path), "/", "_")
		name := strings.ReplaceAll(path.Base(u.Path), ":", "_")

		dir, err := m.createTempDir(parent, name)
		if err != nil {
			return err
		}

		info.Url = dir
	}
	return nil
}

func (ds *Datastore) SearchDatastoreTask(ctx *Context, req *types.SearchDatastore_Task) soap.HasFault {
	task := CreateTask(ds, "searchDatastore", func(*Task) (types.AnyType, types.BaseMethodFault) {
		searchSpec := req.SearchSpec
		results := &types.HostDatastoreBrowserSearchResults{
			Datastore:  &ds.Self,
			FolderPath: req.DatastorePath,
		}

		// Simulate file search
		basePath := ds.Info.GetDatastoreInfo().Url
		searchPath := filepath.Join(basePath, req.DatastorePath)
		err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Match files based on the search spec
			if matchFile(info, searchSpec) {
				fileInfo := types.FileInfo{
					Path:         strings.TrimPrefix(path, basePath),
					FileSize:     info.Size(),
					Modification: types.NewTime(info.ModTime()),
				}
				results.File = append(results.File, &fileInfo)
			}
			return nil
		})

		if err != nil {
			return nil, &types.InvalidDatastorePath{DatastorePath: req.DatastorePath}
		}

		return results, nil
	})

	return &methods.SearchDatastore_TaskBody{
		Res: &types.SearchDatastore_TaskResponse{
			Returnval: task.Run(ctx),
		},
	}
}

func matchFile(info os.FileInfo, spec *types.HostDatastoreBrowserSearchSpec) bool {
	for _, pattern := range spec.MatchPattern {
		if matched, _ := filepath.Match(pattern, info.Name()); matched {
			return true
		}
	}
	return false
}

func parseDatastorePath(dsPath string) (*object.DatastorePath, types.BaseMethodFault) {
	var p object.DatastorePath

	if p.FromString(dsPath) {
		return &p, nil
	}

	return nil, &types.InvalidDatastorePath{DatastorePath: dsPath}
}

func (ds *Datastore) RefreshDatastore(*types.RefreshDatastore) soap.HasFault {
	r := &methods.RefreshDatastoreBody{}

	_, err := os.Stat(ds.Info.GetDatastoreInfo().Url)
	if err != nil {
		r.Fault_ = Fault(err.Error(), &types.HostConfigFault{})
		return r
	}

	info := ds.Info.GetDatastoreInfo()

	info.Timestamp = types.NewTime(time.Now())

	r.Res = &types.RefreshDatastoreResponse{}
	return r
}

func (ds *Datastore) DestroyTask(ctx *Context, req *types.Destroy_Task) soap.HasFault {
	task := CreateTask(ds, "destroy", func(*Task) (types.AnyType, types.BaseMethodFault) {
		if len(ds.Vm) != 0 {
			return nil, &types.ResourceInUse{
				Type: ds.Self.Type,
				Name: ds.Name,
			}
		}

		for _, mount := range ds.Host {
			host := ctx.Map.Get(mount.Key).(*HostSystem)
			ctx.Map.RemoveReference(ctx, host, &host.Datastore, ds.Self)
			parent := hostParent(&host.HostSystem)
			ctx.Map.RemoveReference(ctx, parent, &parent.Datastore, ds.Self)
		}

		p, _ := asFolderMO(ctx.Map.Get(*ds.Parent))
		folderRemoveChild(ctx, p, ds.Self)

		return nil, nil
	})

	return &methods.Destroy_TaskBody{
		Res: &types.Destroy_TaskResponse{
			Returnval: task.Run(ctx),
		},
	}
}
