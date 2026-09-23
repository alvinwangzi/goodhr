// 本文件编排自动回复的 AI 决策、防重和发送状态，不包含平台页面差异。
package positionrunner

import (
 "context"
 "database/sql"
 "errors"
 "fmt"
 "path/filepath"
 "strings"
 "time"
 "unicode/utf8"

 "goodhr5/local-agent-go/internal/cloudapi"
 "goodhr5/local-agent-go/internal/localai"
 "goodhr5/local-agent-go/internal/localdb"
 "goodhr5/local-agent-go/internal/platformcore"
 "goodhr5/local-agent-go/internal/platforms"
)

// autoReplyRoundInterval 是相邻自动回复轮次之间的等待时间。
const autoReplyRoundInterval = 3 * time.Second

// errReplyStorage 表示无法保存发送状态，必须终止任务。
var errReplyStorage = errors.New("自动回复记录保存失败，任务已停止")

// replyGenerator 复用当前 AI 客户端，允许编排只依赖正文生成能力。
type replyGenerator interface { GenerateReply(context.Context, localai.ReplyRequest) (string,error) }

// replyFlow 保存本轮固定依赖，不在公共流程按平台名称分支。
type replyFlow struct {
	db               *localdb.DB
	runtime          platformcore.AutoReplyRuntime
	exec             platformcore.Executor
	generator        replyGenerator
	aiClient         *localai.Client
	target           platformcore.ReplyTarget
	scope, platform, positionID, runID string
	request          localai.ReplyRequest
	rejectTemplate   string // 岗位自定义拒绝话术，留空用系统默认
	cloudClient      *cloudapi.Client
	token            string
	positionSnapshot map[string]any // 岗位快照，供回复后索要简历的 AI 评估使用
	screenshotsDir   string         // 截图目录，供在线简历截图使用
}

// normalizeTaskType 保持省略时为打招呼，拒绝未知流程。
// 支持逗号分隔的多选值（如 "greeting,auto_reply"），返回第一个有效值作为主类型。
func normalizeTaskType(value string) (string,error) {
 value=strings.TrimSpace(value)
 if value=="" { value="greeting" }
 // 多选时取第一个有效类型作为主类型，其余类型由 lifecycle 串行调度。
 if idx:=strings.Index(value,",");idx>0 { value=strings.TrimSpace(value[:idx]) }
 if value!="greeting" && value!="auto_reply" { return "",fmt.Errorf("不支持的任务类型") }
 return value,nil
}

// parseTaskTypes 解析逗号分隔的任务类型列表，返回去重后的有效类型。
func parseTaskTypes(value string) []string {
 value=strings.TrimSpace(value)
 if value=="" { return []string{"greeting"} }
 parts:=strings.Split(value,",")
 seen:=map[string]bool{}
 var result []string
 for _,part:=range parts {
  part=strings.TrimSpace(part)
  if part=="" || seen[part] { continue }
  if part!="greeting" && part!="auto_reply" { continue }
  seen[part]=true
  result=append(result,part)
 }
 if len(result)==0 { return []string{"greeting"} }
 return result
}

// hasTaskType 判断任务类型列表是否包含指定类型。
func hasTaskType(types []string, target string) bool {
 for _,t:=range types { if t==target { return true } }
 return false
}

// process 对一个已读取会话执行新消息判断、生成、复核、落库、发送和页面确认。
func (f *replyFlow) process(ctx context.Context, current platformcore.ReplyContext) (string,error) {
 if err:=ctx.Err(); err!=nil { return "skipped",err }
 if !platformcore.ReplyPositionMatches(current.Conversation,f.target) { return "skipped",nil }
 inbound:=""
 for i:=len(current.Messages)-1;i>=0;i-- {
  if current.Messages[i].Direction=="inbound" { inbound=platformcore.ReplyMessageFingerprint(current.Conversation.ID,current.Messages[i]);break }
 }
 if inbound=="" { return "skipped",nil }
 key:=localdb.AutoReplyRecord{ProfileScope:f.scope,Platform:f.platform,ConversationID:current.Conversation.ID,InboundFingerprint:inbound}
 existing,err:=f.db.FindAutoReply(ctx,key)
 if err!=nil && !errors.Is(err,sql.ErrNoRows) { return "failed",errReplyStorage }
 if err==nil {
  switch existing.Status {
  case "sent": return "skipped",nil
  case "sending","unknown":
   if existing.Status=="sending" {
    if err:=f.transition(existing.ID,"sending","unknown","interrupted");err!=nil{return "failed",err}
   }
   return f.confirm(ctx,current.Conversation,existing,"unknown")
  case "prepared","obsolete":
  default: return "skipped",nil
  }
 }
 current,err=platformcore.ValidateReplyContext(current,f.target)
 if err!=nil || strings.TrimSpace(current.Draft)!="" { return "skipped",nil }
 request:=f.request
 request.CandidateName=current.Conversation.Name
 // 查表分流：检查打招呼阶段的扫描记录，评分 < 50 的候选人使用拒绝话术。
 if f.cloudClient!=nil && strings.TrimSpace(f.token)!="" {
  screening,screenErr:=f.cloudClient.FindScreeningByName(ctx,f.token,f.positionID,f.platform,current.Conversation.Name)
  if screenErr==nil && screening!=nil && screening.Score<50 {
   request.RejectTemplate=f.rejectTemplate
   if request.RejectTemplate=="" { request.RejectTemplate=defaultRejectTemplate() }
   request.FAQ=nil
  }
 }
 var history strings.Builder
 for _,message:=range current.Messages { fmt.Fprintf(&history,"[%s/%s] %s\n",message.Direction,message.Kind,message.Text) }
 request.History=history.String()
 text,err:=f.generator.GenerateReply(ctx,request)
 if ctx.Err()!=nil { return "skipped",ctx.Err() }
 if err!=nil { return "failed",fmt.Errorf("AI 回复生成失败，未发送") }
 text=strings.TrimSpace(text)
 if text=="" || utf8.RuneCountInString(text)>1000 { return "failed",fmt.Errorf("AI 回复内容不符合发送要求") }
 checked,err:=f.runtime.RecheckReplyContext(ctx,f.exec,f.target,current)
 if err!=nil { return replyOutcome(err),err }
 if strings.TrimSpace(checked.Draft)!="" { return "skipped",nil }
 key.PositionID=f.positionID;key.RunID=f.runID;key.ContextFingerprint=current.Fingerprint;key.ReplyFingerprint=platformcore.ReplyHash(text)
 record,err:=f.db.PrepareAutoReply(ctx,key)
 if errors.Is(err,localdb.ErrAutoReplyConflict) { return "skipped",nil }
 if err!=nil { return "failed",errReplyStorage }
 if err=f.runtime.StageReply(ctx,f.exec,f.target,current,text);err!=nil {
  return f.obsolete(record,"prepared",err)
 }
 if err=ctx.Err();err!=nil { return f.obsolete(record,"prepared",err) }
 if err=f.db.TransitionAutoReply(ctx,record.ID,"prepared","sending","");err!=nil { return "failed",errReplyStorage }
 if err=ctx.Err();err!=nil { return f.obsolete(record,"sending",err) }
 attempted,sendErr:=f.runtime.SendReply(ctx,f.exec,f.target,current,text)
 if !attempted {
  if sendErr==nil { sendErr=platformcore.ErrReplyUnsafe }
  return f.obsolete(record,"sending",sendErr)
 }
 // 发送动作已开始：停止信号不取消必要的结果确认，不再执行新的发送动作。
 return f.confirm(context.WithoutCancel(ctx),current.Conversation,record,"sending")
}

// replyOutcome 将安全跳过与会话错误区分，原始页面或模型错误不进入日志。
func replyOutcome(err error) string {
 if errors.Is(err,platformcore.ErrReplyUnsafe)||errors.Is(err,context.Canceled){return "skipped"}
 return "failed"
}

// transition 使用独立短超时保存发送状态，停止后仍需保存确定结果。
func (f *replyFlow) transition(id,from,to,code string) error {
 ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
 if err:=f.db.TransitionAutoReply(ctx,id,from,to,code);err!=nil{return errReplyStorage}
 return nil
}

// obsolete 只在确认未触发发送动作时废弃旧答案，不自动删除输入草稿。
func (f *replyFlow) obsolete(record localdb.AutoReplyRecord,from string,cause error)(string,error){
 if err:=f.transition(record.ID,from,"obsolete","not_sent");err!=nil{return "failed",err}
 return replyOutcome(cause),cause
}

// confirm 只依据平台页面证据确认发送；无法确认则保存 unknown 并禁止重发。
func (f *replyFlow) confirm(ctx context.Context,c platformcore.ReplyConversation,record localdb.AutoReplyRecord,from string)(string,error){
 ctx,cancel:=context.WithTimeout(ctx,8*time.Second);defer cancel()
 confirmed,err:=f.runtime.ConfirmReply(ctx,f.exec,f.target,c,record.InboundFingerprint,record.ReplyFingerprint)
 if err==nil && confirmed {
  if err=f.transition(record.ID,from,"sent","");err!=nil{return "failed",err}
  return "sent",nil
 }
 if from=="sending" {
  if err=f.transition(record.ID,"sending","unknown","unconfirmed");err!=nil{return "failed",err}
 }
 return "unknown",nil
}

// resumeAfterReplyIfNeeded 在自动回复成功后判断是否需要索要简历。
// 委托给平台能力的 ResumeAfterReply 执行具体判断和动作。
func (f *replyFlow) resumeAfterReplyIfNeeded(ctx context.Context, conversation platformcore.ReplyConversation) (string, error) {
 if f.runtime == nil || f.positionSnapshot == nil {
  return "", nil
 }
 resumeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
 defer cancel()
 return f.runtime.ResumeAfterReply(resumeCtx, f.exec, conversation, f.positionSnapshot, f.aiClient, f.screenshotsDir)
}

// replyGeneratorFor 根据启动配置构建自动回复使用的 AI 客户端。
// options 为岗位运行启动参数，同时返回具体客户端供回复后索要简历的 AI 评估使用。
func replyGeneratorFor(options StartOptions) (replyGenerator, *localai.Client, error) {
 if err:=validateAIConfig(options.AIConfig);err!=nil { return nil,nil,err }
 client:=localai.New(options.AIConfig)
 client.EnableThinking=false
 return client,client,nil
}

// positionReplyPrompt 读取岗位级回复提示词，存于岗位快照 ai_config.reply_prompt。
func positionReplyPrompt(position localdb.Position) string {
 return strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["ai_config"]),"reply_prompt"))
}

// positionRequirement 读取岗位要求文本，供回复提示词使用，缺失时留空。
func positionRequirement(position localdb.Position) string {
 if value:=strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["common_config"]),"requirement"));value!="" { return value }
 return strings.TrimSpace(stringFromMap(position.PositionSnapshot,"requirement"))
}

// positionRejectTemplate 读取岗位自定义拒绝话术，留空时使用系统默认。
func positionRejectTemplate(position localdb.Position) string {
 return strings.TrimSpace(stringFromMap(mapValue(position.PositionSnapshot["ai_config"]),"reply_reject_template"))
}

// positionFAQ 读取岗位常见问答语料，最多返回 10 条。
func positionFAQ(position localdb.Position) []localai.FAQEntry {
 aiConfig:=mapValue(position.PositionSnapshot["ai_config"])
 raw,ok:=aiConfig["reply_faq"].([]any)
 if !ok { return nil }
 var result []localai.FAQEntry
 for _,item:=range raw {
  entry,ok:=item.(map[string]any)
  if !ok { continue }
  q:=strings.TrimSpace(stringFromMap(entry,"q"))
  a:=strings.TrimSpace(stringFromMap(entry,"a"))
  if q=="" || a=="" { continue }
  result=append(result,localai.FAQEntry{Q:q,A:a})
  if len(result)>=10 { break }
 }
 return result
}

// defaultRejectTemplate 返回系统默认拒绝话术。
func defaultRejectTemplate() string {
 return "感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。"
}

// runAutoReplyTask 组装自动回复依赖并执行整轮编排。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数。
func (r *Runner) runAutoReplyTask(ctx context.Context, position localdb.Position, options StartOptions) {
 platformRuntime,err:=platforms.RuntimeFor(position.PlatformID)
 if err!=nil {
  r.failStart(position.ID,err.Error(),options)
  return
 }
 runtime,ok:=platformRuntime.(platformcore.AutoReplyRuntime)
 if !ok {
  r.failStart(position.ID,"当前平台暂不支持 AI 自动回复",options)
  return
 }
 generator,aiClient,err:=replyGeneratorFor(options)
 if err!=nil {
  r.failStart(position.ID,err.Error(),options)
  return
 }
 r.runAutoReply(ctx,position,options,runtime,generator,aiClient)
}

// runAutoReply 执行自动回复任务；浏览器动作只走不重试调用，错误分类决定整任务去留。
// ctx 为运行上下文，position 为岗位运行记录，options 为启动参数，runtime 为平台自动回复能力，generator 为回复生成器，aiClient 为 AI 客户端。
func (r *Runner) runAutoReply(ctx context.Context, position localdb.Position, options StartOptions, runtime platformcore.AutoReplyRuntime, generator replyGenerator, aiClient *localai.Client) platformcore.ReplyStats {
 positionID:=position.ID
 totalRounds:=scanRounds(options)
 stats:=platformcore.ReplyStats{}
 r.updateReplyStats(positionID,stats)
 stopped:=func(message string) {
  r.updateProgress(positionID,Progress{Stage:"stopped",Message:message,TotalRounds:totalRounds})
  _,_=r.db.UpdatePositionStatus(positionID,"stopped")
  r.positionLog(positionID,"info","自动回复停止："+message)
  r.notifyCloudAutoReplyStatus(positionID,options,"stopped",stats)
 }
 // fatal 判断错误是否需要终止整任务，不记录页面或聊天原文。
 fatal:=func(err error) bool {
  if err==nil { return false }
  if isBrowserClosedPositionError(err) { stopped("浏览器已关闭，自动回复已结束"); return true }
  if errors.Is(err,context.Canceled) { stopped("自动回复已按停止请求结束"); return true }
  return false
 }
 r.positionLog(positionID,"info","自动回复启动：正在启动浏览器")
 if _,err:=r.worker.Start(ctx);err!=nil {
  r.failStart(positionID,"浏览器启动失败："+err.Error(),options)
  return stats
 }
 exec:=platformExecutor{runner:r,positionID:positionID,once:true}
 profileName:=positionProfileName(position)
 if _,err:=r.worker.CallOnce(ctx,"/api/v1/browser/start",map[string]any{
  "humanize":true,
  "user_data_dir":filepath.Join(r.profilesDir,profileName),
  "downloads_path":r.browserDownloadDir(),
  "no_script":true,
 });err!=nil {
  r.failStart(positionID,"浏览器启动或显示校准失败："+err.Error(),options)
  return stats
 }
 r.positionLog(positionID,"info","自动回复启动：正在打开消息页并核对岗位")
 if err:=runtime.PrepareReplyPage(ctx,exec);err!=nil {
  r.failStart(positionID,"消息页准备失败："+err.Error(),options)
  return stats
 }
 name:=positionPositionName(position)
 target,err:=runtime.ResolveReplyTarget(ctx,exec,name)
 if err!=nil {
  r.failStart(positionID,"页面岗位核对失败："+err.Error(),options)
  return stats
 }
 flow:=&replyFlow{
  db:r.db,runtime:runtime,exec:exec,generator:generator,aiClient:aiClient,target:target,
  scope:platformcore.ReplyHash("profile:"+safePathName(profileName)),
  platform:strings.ToLower(strings.TrimSpace(position.PlatformID)),
  positionID:positionID,
  runID:options.CloudRunID,
  rejectTemplate:positionRejectTemplate(position),
  cloudClient:cloudapi.New(strings.TrimSpace(options.CloudAPIBase)),
  token:options.Token,
  positionSnapshot:position.PositionSnapshot,
  screenshotsDir:r.screenshotsDir,
  request:localai.ReplyRequest{
   PositionName:name,
   PositionRequirement:positionRequirement(position),
   ReplyPrompt:positionReplyPrompt(position),
   ReplySystemPrompt:options.AIConfig.ReplySystemPrompt,
   FAQ:positionFAQ(position),
  },
 }
 failures:=0
 for round:=1;round<=totalRounds;round++ {
  if err:=ctx.Err();err!=nil { stopped("自动回复已按停止请求结束"); return stats }
  r.updateProgress(positionID,Progress{Stage:"scanning",Message:fmt.Sprintf("自动回复：正在检查未读消息，第 %d/%d 轮",round,totalRounds),Round:round,TotalRounds:totalRounds})
  conversations,err:=runtime.ScanUnreadReplies(ctx,exec,target,100)
  if err!=nil {
   r.failStart(positionID,"未读会话扫描失败："+err.Error(),options)
   return stats
  }
  for _,conversation:=range conversations {
   if err:=ctx.Err();err!=nil { stopped("自动回复已按停止请求结束"); return stats }
   current,err:=runtime.ReadReplyContext(ctx,exec,target,conversation)
   if err!=nil {
    stats.Checked++;stats.Failed++;failures++
    r.updateReplyStats(positionID,stats)
    r.positionLog(positionID,"warning","自动回复：会话读取失败，已跳过")
    if fatal(err) { return stats }
   } else {
    outcome,err:=flow.process(ctx,current)
    stats.Checked++
    switch outcome {
    case "sent": stats.Replied++;failures=0
     // 回复成功后判断是否需要索要简历
     if resumeAction,resumeErr:=flow.resumeAfterReplyIfNeeded(ctx,current.Conversation);resumeErr!=nil {
      r.positionLog(positionID,"warning",fmt.Sprintf("自动回复索要简历：动作=%s，错误=%s",resumeAction,resumeErr.Error()))
     } else if resumeAction!="" {
      r.positionLog(positionID,"info",fmt.Sprintf("自动回复索要简历：动作=%s，候选人=%s",resumeAction,current.Conversation.Name))
     }
    case "skipped": stats.Skipped++
    case "unknown": stats.Unknown++
    default: stats.Failed++;failures++
    }
    r.updateReplyStats(positionID,stats)
    // 自动回复流程：上报所有遇到的候选人扫描记录。
    r.reportAutoReplyScreening(ctx,position,options,conversation,outcome)
    if errors.Is(err,errReplyStorage) {
     r.failStart(positionID,err.Error(),options)
     return stats
    }
    if fatal(err) { return stats }
   }
   if failures>=3 {
    r.failStart(positionID,"自动回复连续处理失败，任务已停止",options)
    return stats
   }
  }
  if len(conversations)==0 { break }
  if round<totalRounds {
   if err:=sleepWithContext(ctx,autoReplyRoundInterval);err!=nil { stopped("自动回复已按停止请求结束"); return stats }
  }
 }
 if r.isUserStopped(positionID) {
  stopped("自动回复已按停止请求结束")
  return stats
 }
 r.finishAutoReply(positionID,options,stats,totalRounds)
 return stats
}

// finishAutoReply 保存自动回复完成状态并同步云端，不触发收尾求简历。
func (r *Runner) finishAutoReply(positionID string, options StartOptions, stats platformcore.ReplyStats, totalRounds int) {
 message:=fmt.Sprintf("自动回复完成：检查=%d，回复=%d，跳过=%d，失败=%d，未知=%d",stats.Checked,stats.Replied,stats.Skipped,stats.Failed,stats.Unknown)
 r.updateProgress(positionID,Progress{Stage:"completed",Message:message,Round:totalRounds,TotalRounds:totalRounds})
 _,_=r.db.UpdatePositionStatus(positionID,"completed")
 r.positionLog(positionID,"info",message)
 r.notifyCloudAutoReplyStatus(positionID,options,"completed",stats)
}

// notifyCloudAutoReplyStatus 以自动回复任务类型同步云端终态，回复不计入打招呼数量。
func (r *Runner) notifyCloudAutoReplyStatus(positionID string, options StartOptions, status string, stats platformcore.ReplyStats) {
 if strings.TrimSpace(options.Token)=="" { return }
 baseURL:=strings.TrimSpace(options.CloudAPIBase)
 if baseURL=="" { baseURL=strings.TrimSpace(r.cloudAPIBase) }
 if baseURL=="" { baseURL="https://goodhr5.58it.cn" }
 ctx,cancel:=context.WithTimeout(context.Background(),cloudStatsSyncTimeout)
 defer cancel()
 request:=cloudapi.TaskStatusRequest{Status:status,TaskType:"auto_reply",RunID:options.CloudRunID,MachineID:options.MachineID,Skipped:stats.Skipped}
 if _,err:=cloudapi.New(baseURL).SyncTaskStatus(ctx,options.Token,positionID,request);err!=nil {
  r.positionLog(positionID,"warning","自动回复状态同步失败："+err.Error())
 }
}

// reportAutoReplyScreening 异步上报自动回复遇到的候选人扫描记录。
// position 为岗位运行记录，options 为启动参数，conversation 为当前会话，outcome 为处理结果。
func (r *Runner) reportAutoReplyScreening(ctx context.Context, position localdb.Position, options StartOptions, conversation platformcore.ReplyConversation, outcome string) {
 if strings.TrimSpace(options.Token)=="" { return }
 platform:=strings.ToLower(strings.TrimSpace(position.PlatformID))
 // 使用会话 ID 作为平台候选人标识，自动回复场景下无法获取打招呼阶段的名片指纹。
 candidateID:=conversation.ID
 if candidateID=="" { return }
 name:=conversation.Name
 baseURL:=strings.TrimSpace(options.CloudAPIBase)
 if baseURL=="" { baseURL=strings.TrimSpace(r.cloudAPIBase) }
 go func() {
  syncCtx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
  defer cancel()
  record:=cloudapi.ScreeningRecord{
   Platform:platform,
   PlatformCandidateID:candidateID,
   CandidateName:name,
   Status:outcome,
   Source:"auto_reply",
  }
  if err:=cloudapi.New(baseURL).ReportScreenings(syncCtx,options.Token,position.ID,[]cloudapi.ScreeningRecord{record});err!=nil {
   r.positionLog(position.ID,"warning","自动回复扫描记录上报失败："+err.Error())
  }
 }()
}
