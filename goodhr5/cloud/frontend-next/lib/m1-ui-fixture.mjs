/** 本文件提供 HRPlus M1 独立页面验收接口，只使用虚构账号和岗位，不连接业务数据库或招聘页面。 */
import http from "node:http";

const email="m1-qa@example.com";
const position={id:"m1-ui-position",name:"M1 测试岗位",position_name:"Java开发工程师",platform_id:"boss",user_email:email,mode:"keyword",status:"completed",today_greeted_count:2,greeted_count:90,created_at:"2026-10-08T09:00:00+08:00",common_config:{position_name:"Java开发工程师"},filter_config:{mode_default:"keyword"},ai_config:{}};
let cooperative=true;
let lastStart=null;

/** result 输出测试接口的固定事实，未知路径返回空数据，不转发到真实服务。 */
function result(path,body){
 if(path==="/__fixture/old-agent"){cooperative=false;return {ok:true}}
 if(path==="/__fixture/new-agent"){cooperative=true;return {ok:true}}
 if(path==="/__fixture/last-start")return {body:lastStart};
 if(path==="/health")return {ok:true,data:{version:"999.0.0",capabilities:{auto_reply:true,re_greet:true,...(cooperative?{cooperative_actions:true}:{})}}};
 if(path.includes("login-status"))return {has_password:true};
 if(path.includes("agreement-status"))return {agreement_accepted:true};
 if(path.includes("login-password"))return {access_token:"m1-fixture-token",user:{email}};
 if(path==="/api/auth/me")return {user:{email,name:"M1 页面验收",is_admin:true},show_trial_welcome:false};
 if(path==="/api/subscription/status")return {subscription:{active:true,member_type:"pro",member_name:"Pro会员",allow_ai:true,allow_auto_reply:true,remaining_days:30,remaining_seconds:2592000,features:[]}};
 if(path==="/api/positions")return {positions:[position]};
 if(path==="/api/platforms/config/")return {platforms:[{id:"boss",open:true,config_value:{id:"boss",open:true}}],configs:[{id:"boss",open:true,config_value:{id:"boss",open:true}}]};
 if(path==="/api/runtime/config")return {config:{local_agent:[{version:"1.0.0",url_win:"http://127.0.0.1:25284/test-only.exe",url_mac:"http://127.0.0.1:25284/test-only.exe"}]}};
 if(path==="/api/config/effective-ai")return {config:{api_key_set:true}};
 if(path==="/api/ai-wallet")return {wallet:{balance_yuan:100}};
 if(path==="/api/v1/runtime/status")return {node_installed:true,cloakbrowser_installed:true,components:{node_runtime:{installed:true},cloakbrowser:{installed:true}}};
 if(path.endsWith("/run")){lastStart=body;return {running:true}};
 if(path.endsWith("/status")&&path.includes("positions/"))return {status:"completed",running:false,scanned_count:18,greeted_count:2,skipped_count:9,position:{id:position.id,task_type:"greeting,auto_reply,re_greet",reply_stats:{checked:6,replied:4,accepted_resume:1,skipped:1,failed:0,unknown:1},re_greet_stats:{total:3,sent:2,skipped:0,failed:0,unknown:1}},action_dispatch:{local_run_id:"m1-fixture-run",current_action:"done",message_actions_enabled:true,prioritize_reply:true,last_message_check:"2026-10-08T09:30:00+08:00",next_message_check:null,waiting_for_check:false}};
 if(path.includes("/logs"))return {logs:[],task:result(`/api/v1/local/positions/${position.id}/status`)};
 return {ok:true,config:{},invitations:[],data:{}};
}

/** handle 只在回环地址提供接口，禁止访问真实云端或启动浏览器任务。 */
async function handle(req,res){
 res.setHeader("Access-Control-Allow-Origin","http://127.0.0.1:25373");res.setHeader("Access-Control-Allow-Headers","Content-Type,Authorization");res.setHeader("Access-Control-Allow-Methods","GET,POST,OPTIONS");res.setHeader("Content-Type","application/json");
 if(req.method==="OPTIONS"){res.end();return}
 let raw="";for await(const chunk of req)raw+=chunk;let body={};try{body=JSON.parse(raw||"{}")}catch{}
 const path=new URL(req.url,"http://fixture.invalid").pathname;
 res.end(JSON.stringify(result(path,body)));
}
for(const port of [25284,25329]){http.createServer(handle).listen(port,"127.0.0.1",()=>process.stdout.write(`M1 UI fixture listening ${port}\n`))}
