import PocketBase from 'pocketbase';
import './style.css';

const pocketBaseURL=import.meta.env.VITE_PB_URL||'https://pb.tmadhu.me';
const appURL=location.hostname==='chat.tmadhu.me'&&!import.meta.env.VITE_PB_URL?location.origin:pocketBaseURL;
const pb=new PocketBase(appURL);
// The public PocketBase route streams events without the web proxy's buffering.
const realtime=appURL===pocketBaseURL?pb:new PocketBase(pocketBaseURL,pb.authStore);
pb.autoCancellation(false);
const device=localStorage.akDevice||(localStorage.akDevice=crypto.randomUUID());
const $=id=>document.getElementById(id);
const app=$('app');
const logo='<div class="logo"><img src="/mark.svg" alt=""><div>Apun Ka<span class="sub">Communication</span></div></div>';
const glyphs={phone:'<path d="M4 4h4l2 5-3 2a14 14 0 0 0 6 6l2-3 5 2v4c-9 2-18-7-16-16z"/>',video:'<rect x="3" y="6" width="12" height="12" rx="2"/><path d="m15 10 6-3v10l-6-3"/>',attach:'<path d="m8 13 7-7a3 3 0 1 1 4 4l-9 9a5 5 0 0 1-7-7l8-8m-4 9a1.5 1.5 0 0 0 2 2l7-7"/>',mic:'<rect x="9" y="3" width="6" height="12" rx="3"/><path d="M6 11a6 6 0 0 0 12 0m-6 6v4m-3 0h6"/>',gear:'<circle cx="12" cy="12" r="3"/><path d="M12 3v3m0 12v3m-9-9h3m12 0h3m-15-6 2 2m8 8 2 2m0-12-2 2m-8 8-2 2"/>'};
glyphs.micOff='<path d="m3 3 18 18M9 9v3a3 3 0 0 0 5 2m1-4V5a3 3 0 0 0-5.8-1M6 11a6 6 0 0 0 10 4.5M18 11v1m-6 5v4m-3 0h6"/>';
glyphs.videoOff='<path d="m3 3 18 18M9 6h4a2 2 0 0 1 2 2v3m0-1 6-3v10l-3-1.5M3 8v8a2 2 0 0 0 2 2h8a2 2 0 0 0 1.5-.7"/>';
glyphs.hangup='<path d="M3 12a15 15 0 0 1 18 0v4h-5v-3a9 9 0 0 0-8 0v3H3z"/>';
const iconGraphic=name=>`<svg viewBox="0 0 24 24" aria-hidden="true">${glyphs[name]}</svg>`;
const icon=(id,name,label)=>`<button type="button" class="icon" id="${id}" title="${label}" aria-label="${label}">${iconGraphic(name)}</button>`;
const escape=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const usr=()=>pb.authStore.record?.id;
const api=(path,method='GET',body)=>pb.send('/api/app'+path,{method,body});
const col=name=>pb.collection(name);
const value=(id)=>$(id).value.trim();
const deviceHeaders={'X-App-Device':device};
let toastTimer,heartbeat,typingTimer,conversation,conversationList=[],people={},messages=new Map(),reactions=[],membership=[],subscriptionOff=[],call,room,callDialogBusy=false,historyLocked=false,reply,attachment,recording,qualityTimer,notificationDevice,pendingSend,older=true;
let mediaUrls=[];
let chatRequests=[],inboxSequence=0,inboxFocus,inboxTimer,lastInboxRefresh=0;
function toast(text){$('toast').textContent=text;clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('toast').textContent='',4500)}
function reason(e){return e?.status===0&&!e.isAbort?'Could not connect. Check your connection and try again.':e?.response?.message||e?.message||'Please try again.'}
function button(id,fn){const el=$(id);if(el)el.onclick=async()=>{try{await fn()}catch(e){toast(reason(e))}}}
function form(id,fn){const el=$(id);el.onsubmit=async event=>{event.preventDefault();const b=el.querySelector('[type=submit]');if(b)b.disabled=true;el.querySelector('.error')?.replaceChildren();try{await fn()}catch(e){const box=el.querySelector('.error');if(box)box.textContent=reason(e);else toast(reason(e))}finally{if(b)b.disabled=false}}}
const eye='<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12Z"/><circle cx="12" cy="12" r="2.5"/><path class="eye-slash" d="m4 4 16 16"/></svg>';
function field(label,id,type='text',extra=''){
 const input=`<input id="${id}" type="${type}" required ${extra}>`;
 if(type!=='password')return `<label for="${id}">${label}${input}</label>`;
 return `<div class="password-field"><label for="${id}">${label}</label><div class="password-input">${input}<button type="button" data-password="${id}" aria-label="Show password" aria-pressed="false">${eye}</button></div></div>`;
}
app.addEventListener('click',event=>{
 const toggle=event.target.closest('[data-password]');
 if(!toggle)return;
 const input=$(toggle.dataset.password),show=input.type==='password';
 input.type=show?'text':'password';
 toggle.setAttribute('aria-pressed',String(show));
 toggle.setAttribute('aria-label',show?'Hide password':'Show password');
});
function submit(label){return `<button class="primary" type="submit">${label}</button><div class="error" role="alert"></div>`}
const emailField=()=>field('Email address','email','email','autocomplete="username"');
const passFields=()=>field('New password','pw','password','minlength="8" autocomplete="new-password"')+field('Confirm password','pw2','password','minlength="8" autocomplete="new-password"');
function confirmedPassword(){
 const password=$('pw').value,passwordConfirm=$('pw2').value;
 if(password!==passwordConfirm)throw Error('The passwords do not match.');
 if(new TextEncoder().encode(password).length>72)throw Error('Choose a shorter password.');
 return {password,passwordConfirm};
}
const codeField=()=>field('Six-digit code','code','text','inputmode="numeric" pattern="[0-9]{6}" autocomplete="one-time-code"');
const currentPass=()=>field('Current password','pw','password','autocomplete="current-password"');
const getHash=()=>location.hash.slice(1);
function gate(mode='login',token=''){
 const titles={login:'Welcome back',signup:'Create your account',forgot:'Recover your account',otp:'Enter your recovery code',sent:'Check your inbox',reset:'Choose a new password',email:'Confirm your new email'};
 const description={login:'Sign in to your private conversations.',signup:'Use an email address you can access for account recovery.',forgot:'Choose how you want to reset your password.',otp:'Enter the code from your email and choose a new password.',sent:'If the address belongs to an account, a reset link should arrive shortly.',reset:'Your new password will sign out the previous session.',email:'Enter your current password to finish changing your email address.'};
 app.innerHTML=`<section class="gate"><div class="intro">${logo}<div><h1>Good to<br> <em>hear from you.</em></h1><p>A private place to chat, catch up and call your people.</p></div><div class="rings"></div></div><div class="entry"><section class="panel"><h2>${titles[mode]}</h2><p>${description[mode]}</p><form id="authform"></form><div class="gate-links">${mode==='login'?'<button class="link" id="forgot">Forgot your password?</button><button class="link" id="signup">Create an account</button>':'<button class="link" id="back">Back to sign in</button>'}</div></section></div></section>`;
 const f=$('authform');
 if(mode==='login'){
  f.innerHTML=emailField()+field('Password','pw','password','autocomplete="current-password"')+submit('Sign in');
  form('authform',async()=>{await col('users').authWithPassword(value('email'),$('pw').value,{headers:deviceHeaders});await shell()});
 }else if(mode==='signup'){
  f.innerHTML=field('Display name','displayName','text','maxlength="80" autocomplete="name"')+emailField()+passFields()+submit('Create account');
  form('authform',async()=>{
   const body={email:value('email'),name:value('displayName'),...confirmedPassword()};
   const result=await pb.send('/api/app/account/register',{method:'POST',body,headers:deviceHeaders});
   pb.authStore.save(result.token,result.record);
   await shell();
  });
 }else if(mode==='forgot'){
  f.innerHTML=emailField()+'<label>Send me a<select id="method"><option value="link">Password reset link</option><option value="otp">Six-digit recovery code</option></select></label>'+submit('Send recovery email');
  form('authform',async()=>{const email=value('email');if(value('method')==='link'){await col('users').requestPasswordReset(email);gate('sent')}else{await api('/account/recovery/request','POST',{email});gate('otp');$('email').value=email}});
 }else if(mode==='otp'){
  f.innerHTML=emailField()+codeField()+passFields()+submit('Reset password');
  form('authform',async()=>{await api('/account/recovery/confirm','POST',{email:value('email'),code:value('code'),...confirmedPassword()});pb.authStore.clear();gate();toast('Password reset. Sign in with your new password.')});
 }else if(mode==='reset'||mode==='email'){
  f.innerHTML=(mode==='reset'?passFields():currentPass())+submit(mode==='reset'?'Reset password':'Confirm email');
  form('authform',async()=>{if(mode==='reset'){const {password,passwordConfirm}=confirmedPassword();await col('users').confirmPasswordReset(token,password,passwordConfirm)}else await col('users').confirmEmailChange(token,$('pw').value);history.replaceState(null,'','/');pb.authStore.clear();gate();toast(mode==='reset'?'Password reset. Sign in with your new password.':'Email changed. Sign in with your new address.')});
 }else f.hidden=true;
 button('signup',()=>gate('signup'));button('forgot',()=>gate('forgot'));button('back',()=>{history.replaceState(null,'','/');if(pb.authStore.isValid)return shell();gate()});
}

async function signout(remote=false){if(!remote){try{if(notificationDevice)await api('/devices/'+notificationDevice,'DELETE');await api('/account/logout','POST',{})}catch(e){if(e.status!==401)toast(reason(e))}}
 clearInterval(heartbeat);clearTimeout(typingTimer);clearTimeout(qualityTimer);for(const off of subscriptionOff)off();subscriptionOff=[];await stopMedia();
 if(recording?.state==='recording'){recording.onstop=()=>recording.stream.getTracks().forEach(t=>t.stop());recording.stop()}
 fileToken='';fileTokenAt=0;fileTokenRequest=null;
 clearTimeout(inboxTimer);inboxSequence++;inboxFocus=null;conversationList=[];people={};chatRequests=[];lastInboxRefresh=0;
 for(const url of mediaUrls)URL.revokeObjectURL(url);mediaUrls=[];conversation=null;messages.clear();call=null;pendingSend=null;reply=null;attachment=null;notificationDevice=null;
 pb.authStore.clear();gate();if(remote)toast('Your session ended. If you signed in on another device, use that device to continue.');
}

async function shell(){
 app.innerHTML=`<section class="shell"><aside class="side">${logo}<div class="chat-nav"><h2>Chats</h2><div class="chat-nav-actions"><button id="findPeople" type="button">Find people</button><button id="requests" type="button">Requests <span id="requestCount" hidden></span></button></div><nav id="contacts" class="contacts" aria-label="Chats"></nav></div><button class="me" id="settings"><span class="circle">⚙</span><span class="name">Account<span class="sub">Settings</span></span></button></aside><section class="pane"><header class="bar"><span class="circle" id="avatar">–</span><div><strong id="peer">Conversations</strong><span id="status" class="sub">Connecting…</span></div><div class="actions" id="chatActions" hidden>${icon('audio','phone','Start audio call')}${icon('video','video','Start video call')}</div></header><div id="history" hidden><span>Earlier messages are locked on this device.</span><button id="unlock" class="link">Unlock history</button></div><div id="timeline" class="timeline" aria-label="Messages"></div><form id="composer" class="composer" hidden><div id="pending"></div><div class="compose-row">${icon('attach','attach','Attach media')}${icon('voice','mic','Record voice note')}<input id="file" type="file" accept="image/jpeg,image/png,image/webp,audio/*,video/mp4,video/webm" hidden><textarea id="text" aria-label="Message" placeholder="Write a message…" rows="1"></textarea><button class="primary" type="submit">Send</button></div></form></section></section><dialog id="modal"></dialog><section id="call" hidden aria-label="Active call"></section>`;
 conversation=null;
 renderOwnProfile();
 button('settings',settings);button('unlock',unlock);button('audio',()=>startCall('audio'));button('video',()=>startCall('video'));button('attach',()=>$('file').click());button('voice',voice);
 button('findPeople',findPeople);button('requests',openRequests);
 $('file').onchange=()=>{const file=$('file').files[0];if(file?.size>50*1024*1024){toast('Choose a file under 50 MiB.');return}attachment=file;renderPending()};
 $('text').oninput=()=>{if(conversation&&!typingTimer)typingTimer=setTimeout(()=>{typingTimer=null;api('/conversations/'+conversation+'/presence','PUT',{typing:!!value('text')}).catch(()=>{})},900)};
 $('text').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing&&matchMedia('(pointer:fine)').matches){e.preventDefault();$('composer').requestSubmit()}};
 form('composer',async()=>{await send(value('text'),false);$('text').value=''});
 await loadConversations();
 if($('timeline')){clearInterval(heartbeat);heartbeat=setInterval(tick,8000)}
}

async function loadConversations(){
 try{
  const session=await api('/account/session');historyLocked=session.historyLocked;
  await refreshInbox();
  if(conversation)await sync();
  connectRealtime();
  $('timeline').dataset.ready='true';
 }catch(e){if(e.status===401||e.status===403)return signout(true);$('status').textContent='Connection interrupted';toast(reason(e))}
}
async function refreshInbox(preferred){
 if(preferred)inboxFocus=preferred;
 const sequence=++inboxSequence,contacts=$('contacts');
 if(!contacts||!usr())return;
 const result=await api('/inbox');
 if(sequence!==inboxSequence||$('contacts')!==contacts)return;
 const next=result.chats.find(chat=>chat.id===inboxFocus)?.id
  ||result.chats.find(chat=>chat.id===conversation)?.id||result.chats[0]?.id;
 inboxFocus=null;
 lastInboxRefresh=Date.now();
 const contactsChanged=JSON.stringify(conversationList)!==JSON.stringify(result.chats);
 const requestsChanged=JSON.stringify(chatRequests)!==JSON.stringify(result.requests);
 conversationList=result.chats;
 people=Object.fromEntries(conversationList.map(chat=>[chat.id,chat.peer]));
 chatRequests=result.requests;
 if(contactsChanged)renderContacts();
 if(requestsChanged)renderRequests();
 if(next){
  if(next!==conversation)await select(next);
  else if(contactsChanged)renderChatHeader(people[next]);
 }else{
  conversation=null;
  $('peer').textContent='Conversations';
  $('avatar').textContent='–';
  $('composer').hidden=$('chatActions').hidden=$('history').hidden=true;
  $('status').textContent='Connected';
  $('timeline').innerHTML='<div class="empty"><img src="/mark.svg" alt=""><h3>No conversations yet</h3><p>Find a member and send a message request to start chatting.</p><button id="emptyFind" type="button" class="primary">Find people</button></div>';
  button('emptyFind',findPeople);
 }
}
function scheduleInboxRefresh(){
 clearTimeout(inboxTimer);
 inboxTimer=setTimeout(()=>refreshInbox().catch(e=>{if(e.status!==401)toast(reason(e))}),100);
}
const personName=(person,fallback='Conversation')=>person?.name?.trim()||fallback;
function initials(name){return name.trim().split(/\s+/).slice(0,2).map(word=>Array.from(word)[0]).join('').toUpperCase()}
function showAvatar(circle,person,name,thumb='96x96'){
 circle.textContent=initials(name);
 if(!person?.avatar)return;
 const label=circle.textContent;
 fileAccessToken().then(token=>{
  if(!circle.isConnected||circle.textContent!==label)return;
  const image=new Image();
  image.alt='';
  image.referrerPolicy='no-referrer';
  image.onload=()=>{if(circle.isConnected&&circle.textContent===label)circle.replaceChildren(image)};
  image.src=pb.files.getURL({collectionName:'users',id:person.id},person.avatar,{token,thumb});
 }).catch(()=>{});
}
function renderOwnProfile(){
 const me=pb.authStore.record, name=personName(me,'Account'), settings=$('settings');
 settings.setAttribute('aria-label','Account settings');
 settings.innerHTML=`<span class="circle"></span><span class="name">${escape(name)}<span class="sub">Settings</span></span>`;
 showAvatar(settings.querySelector('.circle'),me,name);
}
function renderContacts(){
 const list=$('contacts');
 list.innerHTML=conversationList.map(c=>`<button class="contact ${conversation===c.id?'active':''}" data-id="${c.id}"><span class="circle"></span><span>${escape(personName(people[c.id]))}</span></button>`).join('');
 list.querySelectorAll('button').forEach(b=>{
  const person=people[b.dataset.id];
  showAvatar(b.firstElementChild,person,personName(person));
  b.onclick=()=>select(b.dataset.id).catch(e=>toast(reason(e)));
 });
}
async function select(id){
 if(!people[id])return;
 if(conversation===id){await sync();return}
 conversation=id;messages.clear();reactions=[];membership=[];reply=null;attachment=null;pendingSend=null;older=true;
 clearTimeout(typingTimer);typingTimer=null;readKey=null;
 $('text').value=$('file').value='';
 $('composer').hidden=$('chatActions').hidden=false;
 $('history').hidden=!historyLocked;
 $('status').textContent='Connecting…';
 $('timeline').innerHTML='<div class="empty" role="status">Loading messages…</div>';
 renderPending();
 for(const contact of $('contacts').querySelectorAll('.contact'))contact.classList.toggle('active',contact.dataset.id===id);
 renderChatHeader(people[id]);
 await sync();
 if(conversation===id){updatePresence();await refreshCall()}
}
function renderChatHeader(person){
 const name=personName(person),oldAvatar=$('avatar'),avatar=oldAvatar.cloneNode(false);
 oldAvatar.replaceWith(avatar);
 $('peer').textContent=name;showAvatar(avatar,person,name);
}
function findPeople(){
 modal('Find people',field('Name or email address','memberQuery','search','minlength="2" maxlength="255" autocomplete="off"')+submit('Search')+'<div id="memberResults" class="member-results" aria-live="polite"></div>',async()=>{
  const container=$('memberResults'),query=value('memberQuery');
  const members=await api('/members/search?'+new URLSearchParams({q:query}));
  if(!container.isConnected)return;
  container.innerHTML=members.length?members.map(member=>{
   const name=personName(member,'Member');
   const chat=conversationList.find(chat=>chat.peer.id===member.id);
   const request=chatRequests.find(request=>request.peer.id===member.id);
   return `<div class="member-row"><span class="circle">${escape(initials(name))}</span><strong>${escape(name)}</strong><button type="button" data-member="${escape(member.id)}" ${!chat&&request&&!request.incoming?'disabled':''}>${chat?'Open chat':request?(request.incoming?'View request':'Request sent'):'Message request'}</button></div>`;
  }).join(''):'<p class="notice">No members found.</p>';
  for(const control of container.querySelectorAll('[data-member]'))control.onclick=async()=>{
   const member=members.find(member=>member.id===control.dataset.member);
   const chat=conversationList.find(chat=>chat.peer.id===member.id);
   if(chat){$('modal').close();await select(chat.id).catch(e=>toast(reason(e)));return}
   if(chatRequests.some(request=>request.peer.id===member.id)){openRequests();return}
   composeRequest(member);
  };
 });
}
function composeRequest(member){
 const name=personName(member,'Member');
 modal('Message request',`<p class="notice">Send a request to <strong>${escape(name)}</strong>.</p><label for="requestNote">First message (optional)<textarea id="requestNote" maxlength="500" rows="3" placeholder="Say hello…"></textarea></label>`+submit('Send request'),async()=>{
  const result=await api('/message-requests','POST',{recipient:member.id,note:value('requestNote')});
  $('modal').close();
  await refreshInbox(result.conversation);
  if(result.direction==='incoming')openRequests();
  else if(result.request)toast('Message request sent.');
 });
}
function openRequests(){
 modal('Message requests','<div id="requestList" class="request-list" aria-live="polite"></div>');
 renderRequests();
}
function renderRequests(){
 const count=chatRequests.filter(request=>request.incoming).length,badge=$('requestCount');
 if(!badge)return;
 badge.hidden=count===0;badge.textContent=count;
 const list=$('requestList');
 if(!list)return;
 list.innerHTML=[true,false].map(incoming=>{
  const rows=chatRequests.filter(request=>request.incoming===incoming);
  return `<section><h3>${incoming?'Received':'Sent'}</h3>${rows.length?rows.map(request=>{
   const name=personName(request.peer,'Member');
   return `<article class="request-card" data-request="${escape(request.id)}"><div class="member-row"><span class="circle">${escape(initials(name))}</span><strong>${escape(name)}</strong></div>${request.note?`<p>${escape(request.note)}</p>`:''}<div class="request-controls">${incoming?'<button type="button" class="primary" data-action="accept">Accept</button>':''}<button type="button" data-action="remove">${incoming?'Decline':'Cancel request'}</button></div></article>`;
  }).join(''):'<p class="notice">No pending requests.</p>'}</section>`;
 }).join('');
 list.onclick=async event=>{
  const control=event.target.closest('[data-action]');
  if(!control)return;
  const card=control.closest('[data-request]');
  const buttons=[...card.querySelectorAll('button')];
  buttons.forEach(button=>button.disabled=true);
  const path='/message-requests/'+card.dataset.request;
  try{
   if(control.dataset.action==='accept'){
    const result=await api(path+'/accept','POST',{});
    $('modal').close();
    await refreshInbox(result.conversation);
   }else{
    await api(path,'DELETE');
    await refreshInbox();
   }
  }catch(e){
   toast(reason(e));
   if(e.status===404)await refreshInbox().catch(()=>{});
  }finally{buttons.forEach(button=>button.disabled=false)}
 };
}
const filter=id=>pb.filter('conversation = {:id}',{id});
async function historyPage(mode='latest'){
 const id=conversation;
 if(!id)return;
 const first=ordered()[0];
 const bounds=mode==='older'&&first?' && '+pb.filter('(created < {:date} || (created = {:date} && id < {:key}))',{date:first.created,key:first.id}):'';
 const page=await col('messages').getList(1,30,{sort:'-created,-id',filter:filter(id)+bounds,skipTotal:true});
 if(id!==conversation)return;
 if(mode==='older'||messages.size===0)older=page.items.length===30;
 page.items.forEach(m=>messages.set(m.id,m));
}
async function catchUp(id){
 const high=ordered().at(-1);
 if(!high){await historyPage();return}
 let after=high;
 while(id===conversation){
  const next=pb.filter('(created > {:date} || (created = {:date} && id > {:key}))',{date:after.created,key:after.id});
  const {items}=await col('messages').getList(1,30,{sort:'created,id',filter:filter(id)+' && '+next,skipTotal:true});
  if(id!==conversation)return;
  for(const m of items)messages.set(m.id,m);
  if(items.length<30)return;
  after=items.at(-1);
 }
}
async function sync(){
 const id=conversation;if(!id)return;
 await catchUp(id);
 if(conversation!==id)return;
 await historyPage();
 if(conversation!==id)return;
 const [r,m]=await Promise.all([loadReactions(id),col('conversation_members').getFullList({filter:filter(id)})]);
 if(conversation!==id)return;
 reactions=r;membership=m;render(true);markRead();
}
function ordered(){return [...messages.values()].sort((a,b)=>a.created.localeCompare(b.created)||a.id.localeCompare(b.id))}
async function loadReactions(id){
 const first=ordered()[0];
 if(!first)return [];
 return col('message_reactions').getFullList({filter:filter(id)+' && '+pb.filter('message.created >= {:first}',{first:first.created})});
}
const commonReactions=[['👍','Like'],['👎','Dislike'],['❤️','Love'],['😂','Haha'],['😮','Wow'],['😢','Sad'],['😡','Angry']];
const emojiPattern=/^(?:\p{Regional_Indicator}{2}|[0-9#*]\uFE0F?\u20E3|\p{Extended_Pictographic}[\uFE0F\p{Emoji_Modifier}\u{E0020}-\u{E007F}]*(?:\u200D\p{Extended_Pictographic}[\uFE0F\p{Emoji_Modifier}\u{E0020}-\u{E007F}]*)*)$/u;
function reactionChips(rows){
 const groups=new Map();
 for(const row of rows){
  const group=groups.get(row.emoji)||{count:0,mine:false};
  group.count++;
  group.mine||=row.user===usr();
  groups.set(row.emoji,group);
 }
 return [...groups].map(([emoji,{count,mine}])=>`<button type="button" class="reaction-chip" data-op="choose-reaction" data-emoji="${escape(emoji)}" aria-pressed="${mine}" aria-label="${escape(emoji)} ${count} ${count===1?'reaction':'reactions'}">${escape(emoji)} ${count}</button>`).join('');
}
function render(stick=false){
 const box=$('timeline');if(!box)return;
 const bottom=stick||box.scrollHeight-box.scrollTop-box.clientHeight<100;
 const scroll=box.scrollTop;
 const previous=box.scrollHeight;
 const items=ordered();
 const byMessage=new Map();
 for(const row of reactions){
  if(!byMessage.has(row.message))byMessage.set(row.message,[]);
  byMessage.get(row.message).push(row);
 }
 const near=membership.find(x=>x.user!==usr());
 if(near){
  const now=Date.now();
  $('status').textContent=Date.parse(near.typing_until)>now?'Typing…':Date.parse(near.presence_until)>now?'Online':'Connected';
 }
 box.innerHTML=(older&&items.length?'<button id="older" class="link">Load earlier messages</button>':'')+(items.length?items.map(m=>{
  const own=m.sender===usr(),last=near&&(m.created<near.read_through||m.created===near.read_through&&m.id<=near.read_id);
  const clock=new Date(m.created).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
  const replyMsg=messages.get(m.reply_to);
  return `<article class="message ${own?'self':''}" data-id="${m.id}">${m.reply_to?`<div class="replyline">${escape(replyMsg?.text||'Earlier message')}</div>`:''}${m.kind==='image'?`<button data-op="file"><img data-preview="${m.id}" alt="Shared image" loading="lazy"></button>`:m.attachment?`<button class="link" data-op="file">${m.kind==='voice'?'Play voice note':'Play video'}</button>`:''}<p>${escape(m.text)}</p><div class="bubble-actions"><small>${escape(clock)}${own&&last?' · Read':''}</small><button data-op="reply">Reply</button>${own?'<button data-op="edit">Edit</button>':''}<button data-op="react" aria-label="Add reaction">React</button></div>${byMessage.has(m.id)?`<div class="reaction-chips">${reactionChips(byMessage.get(m.id))}</div>`:''}</article>`
 }).join(''):'<div class="empty"><img src="/mark.svg" alt=""><h3>Say hello</h3><p>Send a message, share a voice note or start a call.</p></div>');
 button('older',async()=>{const id=conversation;await historyPage('older');const rows=await loadReactions(id);if(id!==conversation)return;reactions=rows;render();box.scrollTop=scroll+box.scrollHeight-previous});
 box.onclick=e=>{const target=e.target.closest('[data-op]');if(target)chatAction(target.dataset.op,target.closest('[data-id]').dataset.id,target.dataset.emoji).catch(err=>toast(reason(err)))};
 if(bottom)box.scrollTop=box.scrollHeight;else box.scrollTop=scroll;
 if(items.some(x=>x.kind==='image'))loadPreviews(box);
 const overlay=$('recent');if(overlay)overlay.innerHTML=items.slice(-3).map(x=>`<p><b>${x.sender===usr()?'You':escape(personName(people[conversation]))}</b> ${escape(x.text||x.kind)}</p>`).join('');
}
let fileToken='',fileTokenAt=0,fileTokenRequest;
async function fileAccessToken(){
 if(fileToken&&Date.now()-fileTokenAt<65000)return fileToken;
 if(!fileTokenRequest){
  const authToken=pb.authStore.token;
  const request=pb.files.getToken().then(token=>{
   if(pb.authStore.token===authToken){fileToken=token;fileTokenAt=Date.now()}
   return token;
  }).finally(()=>{if(fileTokenRequest===request)fileTokenRequest=null});
  fileTokenRequest=request;
 }
 return fileTokenRequest;
}
async function fileURL(m,thumb){return pb.files.getURL(m,m.attachment,{token:await fileAccessToken(),...(thumb?{thumb:'400x0'}:{})})}
async function loadPreviews(box){for(const img of box.querySelectorAll('[data-preview]')){try{const url=await fileURL(messages.get(img.dataset.preview),true);if(img.isConnected)img.src=url}catch{}}}
async function chatAction(op,id,emoji){
 const m=messages.get(id);if(!m)return;
 if(op==='reply'){reply=m;renderPending();$('text').focus();return}
 if(op==='edit'){modal('Edit message',`<label>Message<textarea id="editText" required></textarea></label>`+submit('Save edit'),async()=>{const result=await api('/messages/'+id,'PATCH',{text:value('editText')});messages.set(id,result);$('modal').close();render()});$('editText').value=m.text;return}
 if(op==='react'){reactionPicker(id);return}
 if(op==='choose-reaction'){await setReaction(id,emoji);return}
 if(op==='file'){const src=escape(await fileURL(m,false));modal('Attachment',m.kind==='image'?`<img style="width:100%" src="${src}" alt="Shared image">`:m.kind==='voice'?`<audio controls autoplay src="${src}"></audio>`:`<video controls autoplay style="width:100%" src="${src}"></video>`)}
}
function reactionPicker(id){
 const mine=reactions.find(row=>row.message===id&&row.user===usr())?.emoji;
 modal('React to message',`<div class="emoji-picker">${commonReactions.map(([emoji,label],i)=>`<button type="button" id="emoji-${i}" title="${label}" aria-label="${label}" aria-pressed="${emoji===mine}">${emoji}</button>`).join('')}</div>
 ${field('Use your keyboard’s emoji picker','customEmoji','text','maxlength="48" placeholder="Add an emoji"')}
 ${submit('React')}${mine?'<button type="button" class="link" id="removeReaction">Remove my reaction</button>':''}`,async()=>{
  const emoji=value('customEmoji');
  if(!emojiPattern.test(emoji)||Array.from(emoji).length>24)throw Error('Enter one emoji.');
  await setReaction(id,emoji);
  $('modal').close();
 });
 commonReactions.forEach(([emoji],i)=>button('emoji-'+i,async()=>{await setReaction(id,emoji);$('modal').close()}));
 button('removeReaction',async()=>{await setReaction(id,'');$('modal').close()});
}
const pendingReactions=new Set();
async function setReaction(id,emoji){
 if(pendingReactions.has(id))return;
 const owner=usr(),current=conversation;
 const query=pb.filter('message = {:id} && user = {:user}',{id,user:owner});
 const mine=reactions.find(row=>row.message===id&&row.user===owner)?.emoji;
 pendingReactions.add(id);
 try{
  await api('/reactions/'+id,'PUT',{emoji:emoji===mine?'':emoji});
  const {items}=await col('message_reactions').getList(1,1,{filter:query,skipTotal:true});
  if(conversation!==current||usr()!==owner)return;
  reactions=reactions.filter(row=>row.message!==id||row.user!==owner).concat(items);
  render();
 }finally{pendingReactions.delete(id)}
}
function renderPending(){$('pending').innerHTML=(reply?`<div class="notice">Replying to ${escape((reply.text||reply.kind).slice(0,90))} <button id="clearReply" type="button" class="link">Remove</button></div>`:'')+(attachment?`<div class="notice">${escape(attachment.name)} <button id="clearFile" type="button" class="link">Remove</button></div>`:'');button('clearReply',()=>{reply=null;renderPending()});button('clearFile',()=>{attachment=null;$('file').value='';renderPending()})}
async function send(text,inCall){
 if(!conversation||!text&&!attachment)return;
 const file=inCall?null:attachment;
 const key=[conversation,text,file?.name||'',reply?.id||'',inCall].join('|');
 const nonce=pendingSend?.key===key?pendingSend.nonce:crypto.randomUUID();pendingSend={key,nonce};
 const fields={conversation,text,client_message_id:nonce,kind:file?(file.type.startsWith('image/')?'image':file.type.startsWith('audio/')?'voice':'video'):'text',reply_to:reply?.id||'',in_call:inCall};
 const body=file?new FormData():fields;
 if(file){for(const [k,v] of Object.entries(fields))body.set(k,String(v));body.set('attachment',file)}
 const result=await api('/messages','POST',body);
 if(conversation!==fields.conversation||!$('pending'))return;
 messages.set(result.id,result);
 pendingSend=null;if(!inCall){reply=null;attachment=null;$('file').value='';renderPending()}render(true);
}
let readKey;
function markRead(){if(document.hidden||!conversation)return;const list=ordered();const last=list.at(-1);if(last&&last.id!==readKey){readKey=last.id;api('/conversations/'+conversation+'/read','PUT',{message:last.id}).catch(()=>readKey=null)}}
let syncing=false,changingPassword=false;
async function tick(){if(syncing||changingPassword||!usr()||!$('timeline'))return;const token=pb.authStore.token;syncing=true;try{
 const s=await api('/account/session');if(historyLocked!==s.historyLocked){historyLocked=s.historyLocked;$('history').hidden=!conversation||!historyLocked;messages.clear();await sync()}
 connectRealtime();
 if(!realtime.realtime.isConnected||Date.now()-lastInboxRefresh>30000)await refreshInbox();
 if(conversation&&!document.hidden)await api('/conversations/'+conversation+'/presence','PUT',{typing:false});
 if(conversation){if(!realtime.realtime.isConnected)await sync();await refreshCall()}
}catch(e){if((e.status===401||e.status===403)&&token===pb.authStore.token&&!changingPassword)await signout(true)}finally{syncing=false}}
let subscriptionJob,subscriptionToken;
function connectRealtime(){
 if(!usr()||subscriptionJob)return;
 if(subscriptionToken!==pb.authStore.token){
  for(const off of subscriptionOff)off();
  subscriptionOff=[];
 }
 if(subscriptionOff.length)return;
 subscriptionJob=subscribe().catch(()=>{}).finally(()=>{subscriptionJob=null});
}
async function subscribe(){
 const token=pb.authStore.token,subscribed=[];
 try{
  for(const n of ['messages','conversation_members','message_reactions','calls','conversations','message_requests']){
   if(pb.authStore.token!==token)return;
   subscribed.push(await realtime.collection(n).subscribe('*',evt=>{
    if(pb.authStore.token!==token)return;
    const r=evt.record;
    if(n==='conversations'||n==='message_requests'){scheduleInboxRefresh();return}
    if(n==='calls'){if(r.caller===usr()||r.receiver===usr())applyCall(r).catch(e=>toast(reason(e)));return}
    if(r.conversation!==conversation)return;
    if(n==='messages'){if(evt.action==='delete')messages.delete(r.id);else messages.set(r.id,r);render();markRead()}
    else{const list=n==='message_reactions'?reactions:membership;const i=list.findIndex(x=>x.id===r.id);if(i>=0)list.splice(i,1);if(evt.action!=='delete')list.push(r);if(n==='message_reactions')render();else updatePresence()}
   }));
  }
  if(pb.authStore.token!==token)return;
  subscribed.push(await realtime.realtime.subscribe('PB_CONNECT',()=>{
   if(pb.authStore.token!==token)return;
   scheduleInboxRefresh();
   if(conversation)sync().catch(()=>{});
  }));
  if(pb.authStore.token!==token)return;
  subscriptionOff=subscribed;subscriptionToken=token;
  scheduleInboxRefresh();
  if(conversation)sync().catch(()=>{});
 }finally{if(subscriptionOff!==subscribed)for(const off of subscribed)off()}
}
function updatePresence(){const p=membership.find(m=>m.user!==usr());if(p&&$('status'))$('status').textContent=Date.parse(p.typing_until)>Date.now()?'Typing…':Date.parse(p.presence_until)>Date.now()?'Online':'Connected'}
function modal(title,fields,action){
 const d=$('modal');if(d.open)d.close();
 d.innerHTML=`<div class="row"><h2 style="flex:1">${escape(title)}</h2><button type="button" id="closeModal" aria-label="Close">✕</button></div><form id="dialogForm">${fields}</form>`;
 button('closeModal',()=>d.close());if(action)form('dialogForm',action);d.showModal();
}
function settings(){
 const me=pb.authStore.record;
 modal('Account',`<div class="account-profile"><span class="circle" id="accountAvatar"></span><div><strong>${escape(personName(me,'Your profile'))}</strong><span class="sub">${escape(me.email)}</span></div></div>
 <div class="account-actions"><button type="button" id="profileEdit">Edit profile</button><button type="button" id="emailChange">Change email</button><button type="button" id="passwordChange">Change password</button><button type="button" id="notify">Enable browser notifications</button><button type="button" class="danger" id="logout">Sign out</button></div>`);
 showAvatar($('accountAvatar'),me,personName(me,'Your profile'),'192x192');
 button('profileEdit',editProfile);
 button('logout',()=>signout());
 button('notify',push);
 button('emailChange',()=>modal('Change your email',field('New email address','newEmail','email')+submit('Send confirmation link'),async()=>{
  await col('users').requestEmailChange(value('newEmail'));$('modal').close();toast('Confirmation link sent to your new email.');
 }));
 button('passwordChange',()=>modal('Change your password',passFields()+submit('Save new password'),async()=>{
  changingPassword=true;
  try{
   const result=await api('/account/password','POST',confirmedPassword());
   pb.authStore.save(result.token,result.record);
   fileToken='';fileTokenRequest=null;
   $('modal').close();
  }finally{changingPassword=false}
  toast('Password updated.');
  for(const off of subscriptionOff)off();subscriptionOff=[];
  connectRealtime();
  try{await sync()}catch(e){toast(reason(e))}
 }));
}
function editProfile(){
 const me=pb.authStore.record;
 modal('Edit profile',`<div class="account-profile"><span class="circle" id="avatarPreview"></span><div><strong>Profile picture</strong><span class="sub">JPG, PNG, WebP or GIF · up to 3 MiB</span></div></div>
 <label for="profilePhoto">Choose a picture<input id="profilePhoto" type="file" accept="image/jpeg,image/png,image/webp,image/gif"></label>
 ${me.avatar?'<label class="remove-photo"><input type="checkbox" id="removePhoto"> Remove current picture</label>':''}
 ${field('Display name','profileName','text','maxlength="80" autocomplete="name"')}${submit('Save profile')}`,async()=>{
  const name=value('profileName'),photo=$('profilePhoto').files[0];
  if(!name)throw Error('Enter a display name.');
  if(photo?.size>3*1024*1024)throw Error('Choose a picture under 3 MiB.');
  const data=new FormData();
  data.set('name',name);
  if(photo)data.set('avatar',photo);
  else if($('removePhoto')?.checked)data.set('avatar','');
  const updated=await col('users').update(me.id,data);
  pb.authStore.save(pb.authStore.token,{...me,...updated});
  renderOwnProfile();
  settings();
  toast('Profile updated.');
 });
 showAvatar($('avatarPreview'),me,personName(me,'Your profile'),'192x192');
 $('profileName').value=me.name||'';
 $('profilePhoto').onchange=()=>{
  const photo=$('profilePhoto').files[0],preview=$('avatarPreview');
  if(!photo){showAvatar(preview,me,value('profileName')||personName(me,'Your profile'));return}
  if(photo.size>3*1024*1024){toast('Choose a picture under 3 MiB.');$('profilePhoto').value='';return}
  if($('removePhoto'))$('removePhoto').checked=false;
  const image=new Image(),url=URL.createObjectURL(photo);
  image.alt='';
  image.onload=image.onerror=()=>{
   URL.revokeObjectURL(url);
   if(image.naturalWidth&&$('profilePhoto')?.files[0]===photo)preview.replaceChildren(image);
  };
  image.src=url;
 };
 if($('removePhoto'))$('removePhoto').onchange=()=>{
  if($('removePhoto').checked)$('profilePhoto').value='';
  showAvatar($('avatarPreview'),$('removePhoto').checked?null:me,value('profileName')||personName(me,'Your profile'));
 };
}
async function unlock(){modal('Unlock earlier messages','<p class="notice">We’ll send a one-time code to the email on your account.</p>'+submit('Send a code'),async()=>{await api('/account/history/request','POST',{});modal('Enter your email code',codeField()+submit('Unlock history'),async()=>{await api('/account/history/confirm','POST',{code:value('code')});$('modal').close();await tick();toast('Earlier messages unlocked.')})})}
async function push(){
 if(!('serviceWorker' in navigator)||!('PushManager' in window))throw Error('This browser does not support Web Push.');
 const config=await api('/push');if(!config.webVapidPublicKey)throw Error('Web Push is unavailable.');
 if(await Notification.requestPermission()!=='granted')throw Error('Allow notifications in your browser settings.');
 const reg=await navigator.serviceWorker.register('/sw.js');await navigator.serviceWorker.ready;
 const sub=await reg.pushManager.getSubscription()||await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:config.webVapidPublicKey});
 const j=sub.toJSON();try{notificationDevice=(await api('/devices','POST',{platform:'web',endpoint:j.endpoint,...j.keys})).id}catch(e){if(e.status!==409)throw e; await sub.unsubscribe(); const retry=(await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:config.webVapidPublicKey})).toJSON();notificationDevice=(await api('/devices','POST',{platform:'web',endpoint:retry.endpoint,...retry.keys})).id}
 toast('Browser notifications enabled.');
}
async function voice(){
 if(recording?.state==='recording'){recording.stop();$('voice').classList.remove('danger');return}
 if(!navigator.mediaDevices?.getUserMedia||!window.MediaRecorder)throw Error('Voice recording is unavailable in this browser.');
 const stream=await navigator.mediaDevices.getUserMedia({audio:true});const mime=['audio/webm;codecs=opus','audio/mp4','audio/ogg;codecs=opus'].find(t=>MediaRecorder.isTypeSupported(t));
 const rec=new MediaRecorder(stream,mime?{mimeType:mime}:undefined);const chunks=[];
 rec.ondataavailable=e=>{if(e.data.size)chunks.push(e.data)};
 rec.onstop=()=>{stream.getTracks().forEach(t=>t.stop());const type=rec.mimeType.split(';')[0];const ext=type.includes('mp4')?'m4a':type.includes('ogg')?'ogg':'webm';if($('pending')){attachment=new File(chunks,'Voice note.'+ext,{type});renderPending()}recording=null};
 recording=rec;rec.start();$('voice').classList.add('danger');setTimeout(()=>{if(rec.state==='recording'){rec.stop();$('voice')?.classList.remove('danger')}},120000);toast('Recording. Tap the microphone to stop.');
}

const callActive=['ringing','accepted','connected'];
let Room,RoomEvent,Track,VideoQuality,VideoPresets,ConnectionQuality;
let renderCallId,callWait,manualMode='auto';
const callToggle=(id,glyph,label,title)=>`<button type="button" class="call-icon" id="${id}" aria-label="${label}" title="${title}" aria-pressed="false" disabled>${iconGraphic(glyph+'Off')}</button>`;
async function refreshCall(){
 if(!usr())return;
 const who=usr();
 const r=await col('calls').getList(1,1,{sort:'-created',filter:pb.filter('(caller = {:u} || receiver = {:u}) && (status = "ringing" || status = "accepted" || status = "connected")',{u:who}),skipTotal:true});
 if(who!==usr())return;
 if(r.items[0])await applyCall(r.items[0]);else if(call)await applyCall({...call,status:'ended'});
}
async function startCall(kind){
 if(!conversation)throw Error('Select a conversation first.');
 if(call)throw Error('A call is already in progress.');
 await applyCall(await api('/calls','POST',{conversation,kind}));
}
function scheduledEnd(r){
 clearTimeout(callWait);
 if(r.status==='ringing')callWait=setTimeout(()=>refreshCall().catch(()=>{}),Math.max(0,Date.parse(r.ring_expires_at)-Date.now()+750));
}
async function applyCall(r){
 const host=$('call');if(!host)return;
 if(!callActive.includes(r.status)){
  if(call?.id===r.id){call=null;renderCallId=null;clearTimeout(callWait);await stopMedia();host.hidden=true;toast(r.status==='missed'?'Call was not answered.':'Call ended.')}return;
 }
 if(r.status==='ringing'&&Date.parse(r.ring_expires_at)<=Date.now())return;
 call=r;scheduledEnd(r);host.hidden=false;
 const peer=personName(people[r.conversation],'Your contact');
 if(r.status==='ringing'){
  const incoming=r.receiver===usr();
  renderCallId=null;
  if(host.dataset.phase!==r.id){
   host.dataset.phase=r.id;host.classList.remove('min','call-focus');
   host.innerHTML=`<div class="callhead"><strong>${incoming?'Incoming':'Calling'} · ${escape(peer)}</strong><span>${escape(r.kind)} call</span></div><div class="call-controls">${incoming?'<button id="accept">Accept</button><button id="decline" class="hang">Decline</button>':'<button id="decline" class="hang">Cancel</button>'}</div>`;
   button('accept',async()=>{const item=await api('/calls/'+r.id+'/accept','POST',{deviceId:device});await applyCall(item)});
   button('decline',async()=>{await applyCall(await api('/calls/'+r.id+(incoming?'/reject':'/end'),'POST',{}))});
   if(incoming)playChime();
  }return;
 }
 if(renderCallId===r.id)return;
 renderCallId=r.id;host.dataset.phase='';host.classList.remove('min','call-focus');manualMode='auto';
 const focusButton=r.kind==='video'?'<button type="button" id="videoFocus" aria-label="Video focus" aria-pressed="false"></button>':'';
 host.innerHTML=`<div class="callhead"><strong>${escape(peer)}</strong><span id="callState">Connecting…</span><button id="collapse" aria-label="Minimize call" aria-expanded="true" aria-controls="callStage">−</button></div><div class="stage" id="callStage"><div id="remote"></div><div id="local"></div>${focusButton}<form id="incallForm" class="incall" hidden><div id="recent"></div><div class="row" style="flex-wrap:nowrap"><input id="incallText" placeholder="Message during call" aria-label="Message during call"><button type="submit">Send</button></div></form></div><div id="audioMount"></div><div class="call-controls">${callToggle('mute','mic','Microphone','Unmute microphone')}${callToggle('camera','video','Camera','Turn camera on')}<button class="call-extra" id="flip">Flip</button><button class="call-extra" id="share">Share screen</button><button class="call-extra" id="inChat">Chat</button><select class="call-extra" aria-label="Call quality" id="quality"><option value="auto">Auto HD</option><option value="smooth">Smooth</option><option value="audio">Audio only</option></select><button type="button" class="call-icon hang" id="hang" title="End call" aria-label="End call">${iconGraphic('hangup')}</button></div>`;
 const focus=$('videoFocus');
 if(focus){
  focus.onclick=()=>{
   focus.setAttribute('aria-pressed',String(host.classList.toggle('call-focus')));
   focus.focus({preventScroll:true});
  };
  focus.onkeydown=event=>{
   if(event.key==='Escape'&&host.classList.contains('call-focus')){
    event.preventDefault();
    focus.click();
   }
  };
 }
 button('collapse',()=>{
  const minimized=host.classList.toggle('min'),control=$('collapse');
  control.textContent=minimized?'+':'−';
  control.setAttribute('aria-label',minimized?'Expand call':'Minimize call');
  control.setAttribute('aria-expanded',String(!minimized));
  if(room)for(const p of room.remoteParticipants.values())for(const t of p.videoTrackPublications.values())t.setVideoQuality(minimized||manualMode==='smooth'?VideoQuality.LOW:VideoQuality.HIGH);
 });
 button('hang',async()=>applyCall(await api('/calls/'+r.id+'/end','POST',{})));
 button('inChat',()=>{$('incallForm').hidden=!$('incallForm').hidden;if(!$('incallForm').hidden){render();$('incallText').focus()}});
 form('incallForm',async()=>{if(conversation!==r.conversation)await select(r.conversation);const input=$('incallText');await send(input.value.trim(),true);input.value=''});
 if(callDialogBusy)return;
 callDialogBusy=true;
 try{await connectMedia(r)}catch(e){if($('callState'))$('callState').textContent='Connection failed';renderCallId=null;toast(reason(e))}finally{callDialogBusy=false}
}
function playChime(){try{const context=new AudioContext();const osc=context.createOscillator(),gain=context.createGain();osc.frequency.value=660;gain.gain.setValueAtTime(.08,context.currentTime);gain.gain.exponentialRampToValueAtTime(.001,context.currentTime+.65);osc.connect(gain);gain.connect(context.destination);osc.start();osc.stop(context.currentTime+.7);osc.onended=()=>context.close()}catch{}}
function attachTrack(track,local=false){
 const target=track.kind===Track.Kind.Audio?$('audioMount'):$(local?'local':'remote');if(!target)return;
 if(track.kind===Track.Kind.Audio&&local)return;
 const el=track.attach();if(local)el.muted=true;el.playsInline=true;
 if(track.kind===Track.Kind.Video&&!local){
  const others=[...target.querySelectorAll('video')];if(track.source===Track.Source.ScreenShare)others.forEach(x=>x.hidden=true);
  else if(target.querySelector('[data-screen]'))el.hidden=true;
  if(track.source===Track.Source.ScreenShare)el.dataset.screen='true';
 }
 target.append(el);
}
async function stopMedia(){const old=room;room=null;if(old)await old.disconnect();clearTimeout(qualityTimer)}
function updateCallControls(active){
 if(room!==active||!$('mute')||!$('camera'))return;
 const participant=active.localParticipant;
 for(const [id,glyph,on,title] of [
  ['mute','mic',participant.isMicrophoneEnabled,participant.isMicrophoneEnabled?'Mute microphone':'Unmute microphone'],
  ['camera','video',participant.isCameraEnabled,participant.isCameraEnabled?'Turn camera off':'Turn camera on']
 ]){
  const control=$(id);
  control.querySelector('svg').innerHTML=glyphs[on?glyph:glyph+'Off'];
  control.title=title;
  control.setAttribute('aria-pressed',String(on));
 }
}
function mediaToggle(id,active,change){
 const control=$(id);
 control.disabled=false;
 button(id,async()=>{
  if(room!==active)return;
  control.disabled=true;
  try{await change()}finally{updateCallControls(active);control.disabled=false}
 });
}
async function connectMedia(record,restore){
 ({Room,RoomEvent,Track,VideoQuality,VideoPresets,ConnectionQuality}=await import('livekit-client'));
 await stopMedia();
 const grant=await api('/calls/'+record.id+'/token','POST',{deviceId:device});
 if(call?.id!==record.id)return;
 const active=new Room({adaptiveStream:true,dynacast:true,stopLocalTrackOnUnpublish:true,
  videoCaptureDefaults:{resolution:VideoPresets.h720.resolution},
  publishDefaults:{videoCodec:'h264',backupCodec:{codec:'vp8'},audioPreset:{maxBitrate:32000},dtx:true,red:true}});
 room=active;
 active.on(RoomEvent.TrackSubscribed,t=>attachTrack(t))
  .on(RoomEvent.TrackUnsubscribed,t=>{t.detach().forEach(x=>x.remove());if(!$('remote')?.querySelector('[data-screen]'))$('remote')?.querySelectorAll('video').forEach(x=>x.hidden=false)})
  .on(RoomEvent.LocalTrackPublished,p=>p.track&&attachTrack(p.track,true))
  .on(RoomEvent.LocalTrackUnpublished,p=>p.track?.detach().forEach(x=>x.remove()))
  .on(RoomEvent.Reconnecting,()=>{if($('callState'))$('callState').textContent='Reconnecting…'})
  .on(RoomEvent.Reconnected,()=>{if($('callState'))$('callState').textContent='Connected'})
  .on(RoomEvent.AudioPlaybackStatusChanged,()=>{if(!active.canPlaybackAudio&&$('callState')){$('callState').textContent='Tap to hear';$('callState').onclick=()=>active.startAudio()}})
  .on(RoomEvent.Disconnected,()=>{if(room===active&&call?.id===record.id){
   const desired={mic:active.localParticipant.isMicrophoneEnabled,camera:active.localParticipant.isCameraEnabled};
   if($('callState'))$('callState').textContent='Reconnecting…';
   setTimeout(()=>{if(room===active&&call?.id===record.id)connectMedia(record,desired).catch(e=>{
    renderCallId=null;
    if($('callState'))$('callState').textContent='Reconnecting…';
    toast(reason(e));
   })},2500)
  }})
  .on(RoomEvent.ConnectionQualityChanged,(q,p)=>{if(!p.isLocal||manualMode!=='auto')return;clearTimeout(qualityTimer);qualityTimer=setTimeout(()=>applyQuality(q===ConnectionQuality.Poor?'smooth':'auto').catch(()=>{}),q===ConnectionQuality.Poor?4000:15000)});
 await active.connect(grant.wsUrl,grant.token);
 if(room!==active||!$('callState'))return;
 $('callState').textContent='Connected';await active.startAudio().catch(()=>{});
 await active.localParticipant.setMicrophoneEnabled(restore?.mic??true).catch(()=>toast('Microphone permission is needed to speak.'));
 if(restore?.camera??(record.kind==='video'))await active.localParticipant.setCameraEnabled(true).catch(()=>toast('Camera unavailable. Audio stays connected.'));
 if(room!==active||!$('mute'))return;
 if(manualMode!=='auto'){if($('quality'))$('quality').value=manualMode;await applyQuality(manualMode)}
 if(room!==active||!$('mute'))return;
 updateCallControls(active);
 mediaToggle('mute',active,()=>active.localParticipant.setMicrophoneEnabled(!active.localParticipant.isMicrophoneEnabled));
 mediaToggle('camera',active,async()=>{
  await active.localParticipant.setCameraEnabled(!active.localParticipant.isCameraEnabled);
  if(manualMode==='audio'&&room===active){$('quality').value=manualMode='auto';await applyQuality(manualMode)}
 });
 let facing='user';
 button('flip',async()=>{const pub=active.localParticipant.getTrackPublication(Track.Source.Camera),t=pub?.track;if(t){facing=facing==='user'?'environment':'user';await t.restartTrack({facingMode:facing})}});
 button('share',async()=>{await active.localParticipant.setScreenShareEnabled(!active.localParticipant.isScreenShareEnabled)});
 $('quality').onchange=()=>{manualMode=value('quality');applyQuality(manualMode).catch(e=>toast(reason(e)))};
}
async function applyQuality(mode){
 if(!room)return;
 const active=room,p=active.localParticipant;
 if(mode==='audio'){await p.setCameraEnabled(false)}else{
  const track=p.getTrackPublication(Track.Source.Camera)?.track;
  if(track)await track.restartTrack({resolution:(mode==='smooth'?VideoPresets.h360:VideoPresets.h720).resolution,frameRate:mode==='smooth'?15:30});
 }
 if(room!==active)return;
 updateCallControls(active);
 for(const other of active.remoteParticipants.values())for(const pub of other.videoTrackPublications.values()){
  pub.setSubscribed(mode!=='audio');if(mode!=='audio')pub.setVideoQuality(mode==='smooth'||$('call').classList.contains('min')?VideoQuality.LOW:VideoQuality.HIGH);
 }
}

async function init(){
 const [mode,token]=getHash().split('/');
 if(['email','reset'].includes(mode)&&token){gate(mode,token);return}
 if(pb.authStore.isValid){await shell();return}
 gate();
}
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&usr()){tick();sync().catch(()=>{})}});
window.addEventListener('online',()=>{if(usr()){tick();sync().catch(()=>{})}});
window.addEventListener('hashchange',init);
navigator.serviceWorker?.addEventListener('message',()=>{if(usr()){refreshCall().catch(()=>{});sync().catch(()=>{})}});
init().catch(e=>{gate();toast(reason(e))});

