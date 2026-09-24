// 本文件把已形成的索要意向和已核实的接收、下载事实同步到简历库，不执行招聘页面动作。
package positionrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/platformcore"
)

// resumeTrackingScope 隔离云端环境、平台及浏览器账号，岗位另作独立查询条件。
func (f *replyFlow) resumeTrackingScope() string {
	base := ""
	if f.cloudClient != nil { base = strings.TrimRight(f.cloudClient.BaseURL,"/") }
	raw,_:=json.Marshal([]string{base,f.scope,f.platform})
	return platformcore.ReplyHash(string(raw))
}

// trackResume 先保存在本地再补报；网络失败只影响同步，不回滚已经完成的页面动作。
func (f *replyFlow) trackResume(conversation platformcore.ReplyConversation, state, failure string) {
	if f.db == nil || f.cloudClient == nil || strings.TrimSpace(f.token)=="" || conversation.ID=="" { return }
	identity,_:=json.Marshal([]string{f.scope,f.platform,conversation.ID})
	name:=conversation.Name
	if name=="" { name=conversation.ID }
	payload:=map[string]any{"id":"chat:"+platformcore.ReplyHash(string(identity)),"candidate_name":name,"state":state,"error":failure}
	if f.reviewScore>=0 { payload["score"],payload["reason"]=f.reviewScore,f.reviewReason }
	ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second)
	_,err:=f.db.QueueResumeTracking(ctx,f.resumeTrackingScope(),f.positionID,payload)
	cancel()
	if err!=nil {
		f.flowLog("warning",fmt.Sprintf("简历库本地登记失败：候选人=%s，原因=%s",name,err))
		return
	}
	f.flushResumeTracking()
}

// flushResumeTracking 仅重试数据同步，使用有限等待；登录失效或网络失败时保留本地记录等待下次运行。
func (f *replyFlow) flushResumeTracking() {
	if f.db==nil || f.cloudClient==nil || strings.TrimSpace(f.token)=="" { return }
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	pending,err:=f.db.PendingResumeTracking(ctx,f.resumeTrackingScope(),f.positionID)
	if err!=nil { f.flowLog("warning","读取简历库待同步记录失败："+err.Error()); return }
	for _,item:=range pending {
		if err:=f.cloudClient.SyncResumeTracking(ctx,f.token,item.PositionID,item.Payload); err!=nil {
			f.flowLog("warning","简历库同步未完成，已保留本地记录，下次运行会补报："+err.Error())
			return
		}
		if err:=f.db.MarkResumeTrackingSynced(ctx,item); err!=nil { f.flowLog("warning","简历库同步确认保存失败："+err.Error()); return }
		f.flowLog("info",fmt.Sprintf("简历库已同步：候选人=%s，进度=%s",stringFromMap(item.Payload,"candidate_name"),stringFromMap(item.Payload,"state")))
	}
}
