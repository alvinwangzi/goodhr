// 本文件生成两个虚构会话的受控应用页面，页面自身代码只模拟正常界面交互，测试工具不向招聘页面注入脚本。

/** combinedChatFixture 提供未读回复与到期复打两个独立用户，实际 Worker 仍通过标准输入、点击、搜索和响应读取操作。 */
export function combinedChatFixture(options = {}) {
  const initialDirection = options.ready === false ? 'item-myself' : 'item-friend';
  return `<dl><a href="/web/chat/recommend"><span> 推荐牛人 </span><span hidden>推荐入口提示</span></a><a href="/web/chat/index">沟通</a></dl>
  <div class="job-select"><ul class="ui-dropmenu-list"><li>Go</li></ul></div>
  <div class="chat-message-filter-left"><span onclick="unread=true;renderRows()">未读</span></div>
  <button class="chat-search-btn" onclick="document.querySelector('.chat-job-search').hidden=false;renderSearch()">搜索</button>
  <div class="chat-job-search" hidden><input class="search-input" oninput="renderSearch()"></div><div class="geek-search-list"><ul></ul></div>
  <div class="user-list"></div><div class="chat-conversation"><span class="base-name"></span><span class="source-job">Go</span><div class="chat-message-list"></div><div class="conversation-editor"><input class="boss-chat-editor-input"><button class="submit" onclick="sendFixture()">发送</button></div></div>
  <iframe src="/wapi/zpuser/wap/getUserInfo.json"></iframe><iframe src="/wapi/zprelation/friend/getBossFriendListV2.json"></iframe>
  <script>
  const people=[{id:123,name:'同名候选人 A',direction:${JSON.stringify(initialDirection)},text:'请问岗位还招吗？',sent:[]},{id:124,name:'同名候选人 B',direction:'item-myself',text:'欢迎了解岗位。',sent:[]}];let active=123,unread=false;
  function rowPerson(p){const row=document.createElement('div');row.className='geek-item'+(active===p.id?' selected':'');row.dataset.id=p.id+'-0';row.innerHTML='<span class="geek-name">'+p.name+'</span><span class="source-job">Go</span>';row.onclick=()=>openPerson(p.id);return row}
  function renderRows(){const list=document.querySelector('.user-list');list.replaceChildren();for(const p of people){if(!unread||(p.direction==='item-friend'&&!p.sent.length))list.append(rowPerson(p))}}
  function renderPanel(){const p=people.find(p=>p.id===active);document.querySelector('.base-name').textContent=p.name;const list=document.querySelector('.chat-message-list');list.replaceChildren();for(const [i,m] of [{kind:p.direction,text:p.text},...p.sent.map(text=>({kind:'item-myself',text}))].entries()){const row=document.createElement('div');row.className='message-item';row.innerHTML='<span class="message-time"><span class="time">2026-10-08T09:00:0'+i+'+08:00</span></span>';const text=document.createElement('div');text.className=m.kind;text.textContent=m.text;row.append(text);list.append(row)}}
  function renderSearch(){const value=document.querySelector('.search-input').value;const list=document.querySelector('.geek-search-list ul');list.replaceChildren();for(const p of people.filter(p=>p.name===value)){const row=document.createElement('li');row.innerHTML='<div class="search-right"><span class="content-text">'+p.name+'</span></div>';row.onclick=()=>openPerson(p.id);list.append(row)}}
  async function openPerson(id){unread=false;active=id;document.querySelector('.chat-job-search').hidden=true;document.querySelector('.geek-search-list ul').replaceChildren();renderRows();renderPanel();await fetch('/wapi/zpjob/chat/geek/info?uid='+id)}
  function sendFixture(){const input=document.querySelector('.boss-chat-editor-input');people.find(p=>p.id===active).sent.push(input.value);input.value='';renderPanel();fetch('/fixture/click?uid='+active)}
  renderRows();renderPanel();
  </script>`;
}
