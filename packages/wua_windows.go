/*
Copyright 2019 Google Inc. All Rights Reserved.
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

package packages

import (
	"context"
	"fmt"
	"sync"

	"github.com/GoogleCloudPlatform/osconfig/clog"
	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const (
	S_OK    = 0
	S_FALSE = 1
)

var wuaSession sync.Mutex

// IUpdateSession is a an IUpdateSession.
type IUpdateSession struct {
	*ole.IDispatch
}

func NewUpdateSession() (*IUpdateSession, error) {
	wuaSession.Lock()
	if err := coInitializeEx(); err != nil {
		wuaSession.Unlock()
		return nil, err
	}

	s := &IUpdateSession{}

	unknown, err := oleutil.CreateObject("Microsoft.Update.Session")
	if err != nil {
		s.Close()
		return nil, fmt.Errorf(`oleutil.CreateObject("Microsoft.Update.Session"): %v`, err)
	}
	disp, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		s.Close()
		return nil, fmt.Errorf(`error creating Dispatch object from Microsoft.Update.Session connection: %v`, err)
	}
	s.IDispatch = disp

	return s, nil
}

func (s *IUpdateSession) Close() {
	if s.IDispatch != nil {
		s.IDispatch.Release()
	}
	ole.CoUninitialize()
	wuaSession.Unlock()
}

// InstallWUAUpdate install a WIndows update.
func (s *IUpdateSession) InstallWUAUpdate(ctx context.Context, updt *IUpdate) error {
	title, err := updt.GetProperty("Title")
	if err != nil {
		return fmt.Errorf(`updt.GetProperty("Title"): %v`, err)
	}

	updts, err := NewUpdateCollection()
	if err != nil {
		return err
	}
	defer updts.Release()

	eula, err := updt.GetProperty("EulaAccepted")
	if err != nil {
		return fmt.Errorf(`updt.GetProperty("EulaAccepted"): %v`, err)
	}
	// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-oaut/7b39eb24-9d39-498a-bcd8-75c38e5823d0
	if eula.Val == 0 {
		clog.Debugf(ctx, "%s - Accepting EULA", title.Value())
		if _, err := updt.CallMethod("AcceptEula"); err != nil {
			return fmt.Errorf(`updt.CallMethod("AcceptEula"): %v`, err)
		}
	} else {
		clog.Debugf(ctx, "%s - EulaAccepted: %v", title.Value(), eula.Value())
	}

	if err := updts.Add(updt); err != nil {
		return err
	}

	clog.Debugf(ctx, "Downloading update %s", title.Value())
	if err := s.DownloadWUAUpdateCollection(ctx, updts); err != nil {
		return fmt.Errorf("DownloadWUAUpdateCollection error: %v", err)
	}

	clog.Debugf(ctx, "Installing update %s", title.Value())
	if err := s.InstallWUAUpdateCollection(ctx, updts); err != nil {
		return fmt.Errorf("InstallWUAUpdateCollection error: %v", err)
	}

	return nil
}

func NewUpdateCollection() (*IUpdateCollection, error) {
	updateCollObj, err := oleutil.CreateObject("Microsoft.Update.UpdateColl")
	if err != nil {
		return nil, fmt.Errorf(`oleutil.CreateObject("Microsoft.Update.UpdateColl"): %v`, err)
	}
	defer updateCollObj.Release()

	updateColl, err := updateCollObj.IDispatch(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}

	return &IUpdateCollection{IDispatch: updateColl}, nil
}

type IUpdateCollection struct {
	*ole.IDispatch
}

type IUpdate struct {
	*ole.IDispatch
}

func (c *IUpdateCollection) Add(updt *IUpdate) error {
	if _, err := c.CallMethod("Add", updt.IDispatch); err != nil {
		return fmt.Errorf(`IUpdateCollection.CallMethod("Add", updt): %v`, err)
	}
	return nil
}

func (c *IUpdateCollection) RemoveAt(i int) error {
	if _, err := c.CallMethod("RemoveAt", i); err != nil {
		return fmt.Errorf(`IUpdateCollection.CallMethod("RemoveAt", %d): %v`, i, err)
	}
	return nil
}

func (c *IUpdateCollection) Count() (int32, error) {
	return GetCount(c.IDispatch)
}

func (c *IUpdateCollection) Item(i int) (*IUpdate, error) {
	updtRaw, err := c.GetProperty("Item", i)
	if err != nil {
		return nil, fmt.Errorf(`IUpdateCollection.GetProperty("Item", %d): %v`, i, err)
	}
	return &IUpdate{IDispatch: updtRaw.ToIDispatch()}, nil
}

// GetCount returns the Count property.
func GetCount(dis *ole.IDispatch) (int32, error) {
	countRaw, err := dis.GetProperty("Count")
	if err != nil {
		return 0, fmt.Errorf(`IDispatch.GetProperty("Count"): %v`, err)
	}
	count, _ := countRaw.Value().(int32)

	return count, nil
}

func (u *IUpdate) kbaIDs() ([]string, error) {
	kbArticleIDsRaw, err := u.GetProperty("KBArticleIDs")
	if err != nil {
		return nil, fmt.Errorf(`IUpdate.GetProperty("KBArticleIDs"): %v`, err)
	}
	kbArticleIDs := kbArticleIDsRaw.ToIDispatch()
	defer kbArticleIDs.Release()

	count, err := GetCount(kbArticleIDs)
	if err != nil {
		return nil, err
	}

	if count == 0 {
		return nil, nil
	}

	var ss []string
	for i := 0; i < int(count); i++ {
		item, err := kbArticleIDs.GetProperty("Item", i)
		if err != nil {
			return nil, fmt.Errorf(`kbArticleIDs.GetProperty("Item", %d): %v`, i, err)
		}

		ss = append(ss, item.ToString())
	}
	return ss, nil
}

func (u *IUpdate) categories() ([]string, []string, error) {
	catRaw, err := u.GetProperty("Categories")
	if err != nil {
		return nil, nil, fmt.Errorf(`IUpdate.GetProperty("Categories"): %v`, err)
	}
	cat := catRaw.ToIDispatch()
	defer cat.Release()

	count, err := GetCount(cat)
	if err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, nil
	}

	var cns, cids []string
	for i := 0; i < int(count); i++ {
		itemRaw, err := cat.GetProperty("Item", i)
		if err != nil {
			return nil, nil, fmt.Errorf(`cat.GetProperty("Item", %d): %v`, i, err)
		}
		item := itemRaw.ToIDispatch()
		defer item.Release()

		name, err := item.GetProperty("Name")
		if err != nil {
			return nil, nil, fmt.Errorf(`item.GetProperty("Name"): %v`, err)
		}

		categoryID, err := item.GetProperty("CategoryID")
		if err != nil {
			return nil, nil, fmt.Errorf(`item.GetProperty("CategoryID"): %v`, err)
		}

		cns = append(cns, name.ToString())
		cids = append(cids, categoryID.ToString())
	}
	return cns, cids, nil
}

func (u *IUpdate) moreInfoURLs() ([]string, error) {
	moreInfoURLsRaw, err := u.GetProperty("MoreInfoURLs")
	if err != nil {
		return nil, fmt.Errorf(`IUpdate.GetProperty("MoreInfoURLs"): %v`, err)
	}
	moreInfoURLs := moreInfoURLsRaw.ToIDispatch()
	defer moreInfoURLs.Release()

	count, err := GetCount(moreInfoURLs)
	if err != nil {
		return nil, err
	}

	if count == 0 {
		return nil, nil
	}

	var ss []string
	for i := 0; i < int(count); i++ {
		item, err := moreInfoURLs.GetProperty("Item", i)
		if err != nil {
			return nil, fmt.Errorf(`moreInfoURLs.GetProperty("Item", %d): %v`, i, err)
		}

		ss = append(ss, item.ToString())
	}
	return ss, nil
}

func (c *IUpdateCollection) extractPkg(item int) (*WUAPackage, error) {
	updt, err := c.Item(item)
	if err != nil {
		return nil, err
	}
	defer updt.Release()

	title, err := updt.GetProperty("Title")
	if err != nil {
		return nil, fmt.Errorf(`updt.GetProperty("Title"): %v`, err)
	}

	description, err := updt.GetProperty("Description")
	if err != nil {
		return nil, fmt.Errorf(`updt.GetProperty("Description"): %v`, err)
	}

	kbArticleIDs, err := updt.kbaIDs()
	if err != nil {
		return nil, err
	}

	categories, categoryIDs, err := updt.categories()
	if err != nil {
		return nil, err
	}

	moreInfoURLs, err := updt.moreInfoURLs()
	if err != nil {
		return nil, err
	}

	supportURL, err := updt.GetProperty("SupportURL")
	if err != nil {
		return nil, fmt.Errorf(`updt.GetProperty("SupportURL"): %v`, err)
	}

	lastDeploymentChangeTimeRaw, err := updt.GetProperty("LastDeploymentChangeTime")
	if err != nil {
		return nil, fmt.Errorf(`updt.GetProperty("LastDeploymentChangeTime"): %v`, err)
	}

	lastDeploymentChangeTime, err := ole.GetVariantDate(uint64(lastDeploymentChangeTimeRaw.Val))
	if err != nil {
		return nil, fmt.Errorf(`ole.GetVariantDate(uint64(lastDeploymentChangeTimeRaw.Val)): %v`, err)
	}

	identityRaw, err := updt.GetProperty("Identity")
	if err != nil {
		return nil, fmt.Errorf(`updt.GetProperty("Identity"): %v`, err)
	}
	identity := identityRaw.ToIDispatch()
	defer identity.Release()

	revisionNumber, err := identity.GetProperty("RevisionNumber")
	if err != nil {
		return nil, fmt.Errorf(`identity.GetProperty("RevisionNumber"): %v`, err)
	}

	updateID, err := identity.GetProperty("UpdateID")
	if err != nil {
		return nil, fmt.Errorf(`identity.GetProperty("UpdateID"): %v`, err)
	}

	return &WUAPackage{
		Title:                    title.ToString(),
		Description:              description.ToString(),
		SupportURL:               supportURL.ToString(),
		KBArticleIDs:             kbArticleIDs,
		UpdateID:                 updateID.ToString(),
		Categories:               categories,
		CategoryIDs:              categoryIDs,
		MoreInfoURLs:             moreInfoURLs,
		RevisionNumber:           int32(revisionNumber.Val),
		LastDeploymentChangeTime: lastDeploymentChangeTime,
	}, nil
}

// WUAUpdates queries the Windows Update Agent API searcher with the provided query.
func WUAUpdates(ctx context.Context, query string) ([]WUAPackage, error) {
	session, err := NewUpdateSession()
	if err != nil {
		return nil, fmt.Errorf("error creating NewUpdateSession: %v", err)
	}
	defer session.Close()

	updts, err := session.GetWUAUpdateCollection(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error calling GetWUAUpdateCollection with query %q: %v", query, err)
	}
	defer updts.Release()

	updtCnt, err := updts.Count()
	if err != nil {
		return nil, err
	}

	if updtCnt == 0 {
		return nil, nil
	}

	var packages []WUAPackage
	for i := 0; i < int(updtCnt); i++ {
		pkg, err := updts.extractPkg(i)
		if err != nil {
			return nil, err
		}
		packages = append(packages, *pkg)
	}
	return packages, nil
}

// DownloadWUAUpdateCollection downloads all updates in a IUpdateCollection
func (s *IUpdateSession) DownloadWUAUpdateCollection(ctx context.Context, updates *IUpdateCollection) error {
	// returns IUpdateDownloader
	// https://docs.microsoft.com/en-us/windows/desktop/api/wuapi/nn-wuapi-iupdatedownloader
	downloaderRaw, err := s.CallMethod("CreateUpdateDownloader")
	if err != nil {
		return fmt.Errorf("error calling method CreateUpdateDownloader on IUpdateSession: %v"+GetScodeString(ctx, err), err)
	}
	downloader := downloaderRaw.ToIDispatch()
	defer downloader.Release()

	if _, err := downloader.PutProperty("Updates", updates.IDispatch); err != nil {
		return fmt.Errorf("error calling PutProperty Updates on IUpdateDownloader: %v"+GetScodeString(ctx, err), err)
	}

	if _, err := downloader.CallMethod("Download"); err != nil {
		return fmt.Errorf("error calling method Download on IUpdateDownloader: %v"+GetScodeString(ctx, err), err)
	}
	return nil
}

// InstallWUAUpdateCollection installs all updates in a IUpdateCollection
func (s *IUpdateSession) InstallWUAUpdateCollection(ctx context.Context, updates *IUpdateCollection) error {
	// returns IUpdateInstallersession *ole.IDispatch,
	// https://docs.microsoft.com/en-us/windows/desktop/api/wuapi/nf-wuapi-iupdatesession-createupdateinstaller
	installerRaw, err := s.CallMethod("CreateUpdateInstaller")
	if err != nil {
		return fmt.Errorf("error calling method CreateUpdateInstaller on IUpdateSession: %v"+GetScodeString(ctx, err), err)
	}
	installer := installerRaw.ToIDispatch()
	defer installer.Release()

	if _, err := installer.PutProperty("Updates", updates.IDispatch); err != nil {
		return fmt.Errorf("error calling PutProperty Updates on IUpdateInstaller: %v"+GetScodeString(ctx, err), err)
	}

	// TODO: Look into using the async methods and attempt to track/log progress.
	if _, err := installer.CallMethod("Install"); err != nil {
		return fmt.Errorf("error calling method Install on IUpdateInstaller: %v"+GetScodeString(ctx, err), err)
	}
	return nil
}

// GetWUAUpdateCollection queries the Windows Update Agent API searcher with the provided query
// and returns a IUpdateCollection.
func (s *IUpdateSession) GetWUAUpdateCollection(ctx context.Context, query string) (*IUpdateCollection, error) {
	// returns IUpdateSearcher
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa386515(v=vs.85).aspx
	searcherRaw, err := s.CallMethod("CreateUpdateSearcher")
	if err != nil {
		return nil, fmt.Errorf("error calling CreateUpdateSearcher: %v"+GetScodeString(ctx, err), err)
	}
	searcher := searcherRaw.ToIDispatch()
	defer searcher.Release()

	// returns ISearchResult
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa386077(v=vs.85).aspx
	resultRaw, err := searcher.CallMethod("Search", query)
	if err != nil {
		return nil, fmt.Errorf("error calling method Search on IUpdateSearcher: %v"+GetScodeString(ctx, err), err)
	}
	result := resultRaw.ToIDispatch()
	defer result.Release()

	// returns IUpdateCollection
	// https://msdn.microsoft.com/en-us/library/windows/desktop/aa386107(v=vs.85).aspx
	updtsRaw, err := result.GetProperty("Updates")
	if err != nil {
		return nil, fmt.Errorf("error calling GetProperty Updates on ISearchResult: %v"+GetScodeString(ctx, err), err)
	}

	return &IUpdateCollection{IDispatch: updtsRaw.ToIDispatch()}, nil
}

// GetScodeString return empty string if empty string if SCODE wasn't found else string containgin SCODE the
// following format " SCODE : 0x12345678". Where SCODE is a 32-bit status value that is used to describe an error or warning.
func GetScodeString(ctx context.Context, err error) string {
	var scodeStr = ""
	var oleError *ole.OleError
	oleError, ok := err.(*ole.OleError)
	if ok {
		var excepinfo ole.EXCEPINFO
		excepinfo, ok = oleError.SubError().(ole.EXCEPINFO)
		if ok {
			clog.Errorf(ctx, "Error with SCODE was founded. SCODE : 0x%x", excepinfo.SCODE())
			scodeStr = fmt.Sprintf(" SCODE: 0x%x", excepinfo.SCODE())
		}
	}
	return scodeStr
}
