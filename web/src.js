import PocketBase from 'pocketbase';
import './style.css';

const pb=new PocketBase('https://pb.tmadhu.me');
pb.autoCancellation(false);
const device=localStorage.akDevice||(localStorage.akDevice=crypto.randomUUID());
const $=id=>document.getElementById(id);
const app=$('app');
const logo='<div class="logo"><img src="/mark.svg" alt=""><div>Apun Ka<span class="sub">Communication</span></div></div>';
const glyphs={phone:'<path d="M4 4h4l2 5-3 2a14 14 0 0 0 6 6l2-3 5 2v4c-9 2-18-7-16-16z"/>',video:'<rect x="3" y="6" width="12" height="12" rx="2"/><path d="m15 10 6-3v10l-6-3"/>',attach:'<path d="m8 13 7-7a3 3 0 1 1 4 4l-9 9a5 5 0 0 1-7-7l8-8m-4 9a1.5 1.5 0 0 0 2 2l7-7"/>',mic:'<rect x="9" y="3" width="6" height="12" rx="3"/><path d="M6 11a6 6 0 0 0 12 0m-6 6v4m-3 0h6"/>',gear:'<circle cx="12" cy="12" r="3"/><path d="M12 3v3m0 12v3m-9-9h3m12 0h3m-15-6 2 2m8 8 2 2m0-12-2 2m-8 8-2 2"/>'};
const icon=(id,name,label)=>`<button type="button" class="icon" id="${id}" title="${label}" aria-label="${label}"><svg viewBox="0 0 24 24" aria-hidden="true">${glyphs[name]}</svg></button>`;
const escape=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const usr=()=>pb.authStore.record?.id;
const api=(path,method='GET',body)=>pb.send('/api/app'+path,{method,body});
const col=name=>pb.collection(name);
const value=(id)=>$(id).value.trim();
let toastTimer,heartbeat,typingTimer,conversation,conversationList=[],people={},messages=new Map(),reactions=[],membership=[],subscriptionOff=[],call,room,callDialogBusy=false,historyLocked=false,reply,attachment,recording,qualityTimer,notificationDevice,pendingSend,older=true;
let mediaUrls=[];
function toast(text){$('toast').textContent=text;clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('toast').textContent='',4500)}
function reason(e){return e?.response?.message||e?.message||'Please try again.'}
function button(id,fn){const el=$(id);if(el)el.onclick=async()=>{try{await fn()}catch(e){toast(reason(e))}}}
function form(id,fn){const el=$(id);el.onsubmit=async event=>{event.preventDefault();const b=el.querySelector('[type=submit]');if(b)b.disabled=true;el.querySelector('.error')?.replaceChildren();try{await fn()}catch(e){const box=el.querySelector('.error');if(box)box.textContent=reason(e);else toast(reason(e))}finally{if(b)b.disabled=false}}}
function field(label,id,type='text',extra=''){return `<label>${label}<input id="${id}" type="${type}" required ${extra}></label>`}
function submit(label){return `<button class="primary" type="submit">${label}</button><div class="error" role="alert"></div>`}
const emailField=()=>field('Email address','email','email','autocomplete="username"');
const passFields=()=>field('New password','pw','password','minlength="8" autocomplete="new-password"')+field('Confirm password','pw2','password','minlength="8" autocomplete="new-password"');
const codeField=()=>field('Six-digit code','code','text','inputmode="numeric" pattern="[0-9]{6}" autocomplete="one-time-code"');
const currentPass=()=>field('Current password','pw','password','autocomplete="current-password"');
const getHash=()=>location.hash.slice(1);
function gate(mode='login',token=''){
 const titles={login:'Welcome back',forgot:'Recover your account',otp:'Enter your recovery code',sent:'Check your inbox',reset:'Choose a new password',email:'Confirm your new email'};
 const description={login:'Sign in to your private conversations.',forgot:'Choose how you want to reset your password.',otp:'Enter the code from your email and choose a new password.',sent:'If the address belongs to an account, a reset link should arrive shortly.',reset:'Your new password will sign out the previous session.',email:'Enter your current password to finish changing your email address.'};
 app.innerHTML=`<section class="gate"><div class="intro">${logo}<div><h1>Good to<br> <em>hear from you.</em></h1><p>A private place to chat, catch up and call your people.</p></div><small>Private by invitation</small><div class="rings"></div></div><div class="entry"><section class="panel"><h2>${titles[mode]}</h2><p>${description[mode]}</p><form id="authform"></form>${mode==='login'?'<button class="link" id="forgot">Forgot your password?</button>':'<button class="link" id="back">Back to sign in</button>'}</section></div></section>`;
 const f=$('authform');
 if(mode==='login'){
  f.innerHTML=emailField()+field('Password','pw','password','autocomplete="current-password"')+submit('Sign in');
  form('authform',async()=>{await col('users').authWithPassword(value('email'),$('pw').value,{headers:{'X-App-Device':device}});await shell()});
 }else if(mode==='forgot'){
  f.innerHTML=emailField()+'<label>Send me a<select id="method"><option value="link">Password reset link</option><option value="otp">Six-digit recovery code</option></select></label>'+submit('Send recovery email');
  form('authform',async()=>{const email=value('email');if(value('method')==='link'){await col('users').requestPasswordReset(email);gate('sent')}else{await api('/account/recovery/request','POST',{email});gate('otp');$('email').value=email}});
 }else if(mode==='otp'){
  f.innerHTML=emailField()+codeField()+passFields()+submit('Reset password');
  form('authform',async()=>{await api('/account/recovery/confirm','POST',{email:value('email'),code:value('code'),password:$('pw').value,passwordConfirm:$('pw2').value});pb.authStore.clear();gate();toast('Password reset. Sign in with your new password.')});
 }else if(mode==='reset'||mode==='email'){
  f.innerHTML=(mode==='reset'?passFields():currentPass())+submit(mode==='reset'?'Reset password':'Confirm email');
  form('authform',async()=>{if(mode==='reset')await col('users').confirmPasswordReset(token,$('pw').value,$('pw2').value);else await col('users').confirmEmailChange(token,$('pw').value);history.replaceState(null,'','/');pb.authStore.clear();gate();toast(mode==='reset'?'Password reset. Sign in with your new password.':'Email changed. Sign in with your new address.')});
 }else f.hidden=true;
 button('forgot',()=>gate('forgot'));button('back',()=>{history.replaceState(null,'','/');if(pb.authStore.isValid)return shell();gate()});
}

async function signout(remote=false){if(!remote){try{if(notificationDevice)await api('/devices/'+notificationDevice,'DELETE');await api('/account/logout','POST',{})}catch(e){if(e.status!==401)toast(reason(e))}}
 clearInterval(heartbeat);clearTimeout(typingTimer);clearTimeout(qualityTimer);for(const off of subscriptionOff)off();subscriptionOff=[];await stopMedia();
 if(recording?.state==='recording'){recording.onstop=()=>recording.stream.getTracks().forEach(t=>t.stop());recording.stop()}
 fileToken='';fileTokenAt=0;
 for(const url of mediaUrls)URL.revokeObjectURL(url);mediaUrls=[];conversation=null;messages.clear();call=null;pendingSend=null;reply=null;attachment=null;notificationDevice=null;
 pb.authStore.clear();gate();if(remote)toast('Your session ended. If you signed in on another device, use that device to continue.');
}

async function shell(){
 app.innerHTML=`<section class="shell"><aside class="side">${logo}<div><h2>Chats</h2><div id="contacts" class="contacts"></div></div><button class="me" id="settings"><span class="circle">⚙</span><span class="name">Account<span class="sub">Settings</span></span></button></aside><section class="pane"><header class="bar"><span class="circle" id="avatar">–</span><div><strong id="peer">Conversations</strong><span id="status" class="sub">Connecting…</span></div><div class="actions">${icon('audio','phone','Start audio call')}${icon('video','video','Start video call')}</div></header><div id="history" hidden><span>Earlier messages are locked on this device.</span><button id="unlock" class="link">Unlock history</button></div><div id="timeline" class="timeline" aria-label="Messages"></div><form id="composer" class="composer"><div id="pending"></div><div class="compose-row">${icon('attach','attach','Attach media')}${icon('voice','mic','Record voice note')}<input id="file" type="file" accept="image/jpeg,image/png,image/webp,audio/*,video/mp4,video/webm" hidden><textarea id="text" aria-label="Message" placeholder="Write a message…" rows="1"></textarea><button class="primary" type="submit">Send</button></div></form></section></section><dialog id="modal"></dialog><section id="call" hidden aria-label="Active call"></section>`;
 button('settings',settings);button('unlock',unlock);button('audio',()=>startCall('audio'));button('video',()=>startCall('video'));button('attach',()=>$('file').click());button('voice',voice);
 for(const control of $('composer').querySelectorAll('button,textarea'))control.disabled=true;
 $('file').onchange=()=>{const file=$('file').files[0];if(file?.size>50*1024*1024){toast('Choose a file under 50 MiB.');return}attachment=file;renderPending()};
 $('text').oninput=()=>{if(conversation&&!typingTimer)typingTimer=setTimeout(()=>{typingTimer=null;api('/conversations/'+conversation+'/presence','PUT',{typing:!!value('text')}).catch(()=>{})},900)};
 $('text').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing&&matchMedia('(pointer:fine)').matches){e.preventDefault();$('composer').requestSubmit()}};
 form('composer',async()=>{await send(value('text'),false);$('text').value=''});
 await loadConversations();
 clearInterval(heartbeat);heartbeat=setInterval(tick,8000);
}

async function loadConversations(){
 try{
  const session=await api('/account/session');historyLocked=session.historyLocked;$('history').hidden=!historyLocked;
  conversationList=await col('conversations').getFullList();
  people=Object.fromEntries(await Promise.all(conversationList.map(async c=>{const rows=await api('/conversations/'+c.id+'/people');return [c.id,rows.find(p=>p.id!==usr())?.name||'Conversation']})));
  renderContacts();
  if(conversationList[0])await select(conversationList[0].id);
  else{$('timeline').innerHTML='<div class="empty"><img src="/mark.svg" alt=""><h3>No conversations yet</h3><p>Your administrator can pair your account with another member.</p></div>';$('composer').hidden=true;$('status').textContent='Connected'}
  await subscribe();
  if(conversation){await sync();await refreshCall()}
  for(const control of $('composer').querySelectorAll('button,textarea'))control.disabled=false;
  $('timeline').dataset.ready='true';
 }catch(e){if(e.status===401||e.status===403)return signout(true);$('status').textContent='Connection interrupted';toast(reason(e))}
}
function initials(n){return (n||'?').trim().split(/\s+/).slice(0,2).map(x=>x[0]).join('').toUpperCase()}
function renderContacts(){$('contacts').innerHTML=conversationList.map(c=>`<button class="contact ${conversation===c.id?'active':''}" data-id="${c.id}"><span class="circle">${escape(initials(people[c.id]))}</span><span>${escape(people[c.id])}</span></button>`).join('');$('contacts').querySelectorAll('button').forEach(b=>b.onclick=()=>select(b.dataset.id).catch(e=>toast(reason(e))))}
async function select(id){conversation=id;messages.clear();reply=null;attachment=null;pendingSend=null;older=true;renderPending();renderContacts();$('peer').textContent=people[id];$('avatar').textContent=initials(people[id]);await sync();await refreshCall()}
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
 await historyPage();
 const [r,m]=await Promise.all([col('message_reactions').getList(1,150,{filter:filter(id),sort:'-updated',skipTotal:true}),col('conversation_members').getFullList({filter:filter(id)})]);
 if(conversation!==id)return;
 reactions=r.items;membership=m;render(true);markRead();
}
function ordered(){return [...messages.values()].sort((a,b)=>a.created.localeCompare(b.created)||a.id.localeCompare(b.id))}
function render(stick=false){
 const box=$('timeline');if(!box)return;
 const bottom=stick||box.scrollHeight-box.scrollTop-box.clientHeight<100;
 const scroll=box.scrollTop;
 const previous=box.scrollHeight;
 const items=ordered();
 const near=membership.find(x=>x.user!==usr());
 if(near){
  const now=Date.now();
  $('status').textContent=Date.parse(near.typing_until)>now?'Typing…':Date.parse(near.presence_until)>now?'Online':'Connected';
 }
 box.innerHTML=(older&&items.length?'<button id="older" class="link">Load earlier messages</button>':'')+(items.length?items.map(m=>{
  const own=m.sender===usr(),last=near&&(m.created<near.read_through||m.created===near.read_through&&m.id<=near.read_id);
  const clock=new Date(m.created).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'});
  const replyMsg=messages.get(m.reply_to);
  return `<article class="message ${own?'self':''}" data-id="${m.id}">${m.reply_to?`<div class="replyline">${escape(replyMsg?.text||'Earlier message')}</div>`:''}${m.kind==='image'?`<button data-op="file"><img data-preview="${m.id}" alt="Shared image" loading="lazy"></button>`:m.attachment?`<button class="link" data-op="file">${m.kind==='voice'?'Play voice note':'Play video'}</button>`:''}<p>${escape(m.text)}</p><div class="bubble-actions"><small>${escape(clock)}${own&&last?' · Read':''}</small><button data-op="reply">Reply</button>${own?'<button data-op="edit">Edit</button>':''}<button data-op="react" aria-label="React with a heart">♥ ${reactions.filter(r=>r.message===m.id).map(r=>escape(r.emoji)).join(' ')}</button></div></article>`
 }).join(''):'<div class="empty"><img src="/mark.svg" alt=""><h3>Say hello</h3><p>Send a message, share a voice note or start a call.</p></div>');
 button('older',async()=>{await historyPage('older');render();box.scrollTop=scroll+box.scrollHeight-previous});
 box.onclick=e=>{const target=e.target.closest('[data-op]');if(target)chatAction(target.dataset.op,target.closest('[data-id]').dataset.id).catch(err=>toast(reason(err)))};
 if(bottom)box.scrollTop=box.scrollHeight;else box.scrollTop=scroll;
 if(items.some(x=>x.kind==='image'))loadPreviews(box);
 const overlay=$('recent');if(overlay)overlay.innerHTML=items.slice(-3).map(x=>`<p><b>${x.sender===usr()?'You':escape(people[conversation])}</b> ${escape(x.text||x.kind)}</p>`).join('');
}
let fileToken='',fileTokenAt=0;
async function fileURL(m,thumb){if(Date.now()-fileTokenAt>65000||!fileToken){fileToken=await pb.files.getToken();fileTokenAt=Date.now()}return pb.files.getURL(m,m.attachment,{token:fileToken,...(thumb?{thumb:'400x0'}:{})})}
async function loadPreviews(box){for(const img of box.querySelectorAll('[data-preview]')){try{const url=await fileURL(messages.get(img.dataset.preview),true);if(img.isConnected)img.src=url}catch{}}}
async function chatAction(op,id){
 const m=messages.get(id);if(!m)return;
 if(op==='reply'){reply=m;renderPending();$('text').focus();return}
 if(op==='edit'){modal('Edit message',`<label>Message<textarea id="editText" required></textarea></label>`+submit('Save edit'),async()=>{const result=await api('/messages/'+id,'PATCH',{text:value('editText')});messages.set(id,result);$('modal').close();render()});$('editText').value=m.text;return}
 if(op==='react'){await api('/reactions/'+id,'PUT',{emoji:reactions.some(x=>x.message===id&&x.user===usr())?'':'❤️'});return}
 if(op==='file'){const src=escape(await fileURL(m,false));modal('Attachment',m.kind==='image'?`<img style="width:100%" src="${src}" alt="Shared image">`:m.kind==='voice'?`<audio controls autoplay src="${src}"></audio>`:`<video controls autoplay style="width:100%" src="${src}"></video>`)}
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
let syncing=false;
async function tick(){if(syncing||!usr()||!$('timeline'))return;syncing=true;try{
 const s=await api('/account/session');if(historyLocked!==s.historyLocked){historyLocked=s.historyLocked;$('history').hidden=!historyLocked;messages.clear();await sync()}
 if(!subscriptionOff.length){await loadConversations();return}
 if(conversation&&!document.hidden)await api('/conversations/'+conversation+'/presence','PUT',{typing:false});
 if(conversation){if(!pb.realtime.isConnected)await sync();await refreshCall()}
}catch(e){if(e.status===401||e.status===403)await signout(true)}finally{syncing=false}}
async function subscribe(){
 for(const off of subscriptionOff)off();subscriptionOff=[];
 try{for(const n of ['messages','conversation_members','message_reactions','calls']){
  const off=await col(n).subscribe('*',evt=>{const r=evt.record;
   if(n==='calls'){if(r.caller===usr()||r.receiver===usr())applyCall(r).catch(e=>toast(reason(e)));return}
   if(r.conversation!==conversation)return;
   if(n==='messages'){if(evt.action==='delete')messages.delete(r.id);else messages.set(r.id,r);render();markRead()}
   else{const list=n==='message_reactions'?reactions:membership;const i=list.findIndex(x=>x.id===r.id);if(i>=0)list.splice(i,1);if(evt.action!=='delete')list.push(r);if(n==='message_reactions')render();else updatePresence()}
  });subscriptionOff.push(off)
 }}catch(e){for(const off of subscriptionOff)off();subscriptionOff=[];throw e}
}
function updatePresence(){const p=membership.find(m=>m.user!==usr());if(p&&$('status'))$('status').textContent=Date.parse(p.typing_until)>Date.now()?'Typing…':Date.parse(p.presence_until)>Date.now()?'Online':'Connected'}
function modal(title,fields,action){
 const d=$('modal');if(d.open)d.close();
 d.innerHTML=`<div class="row"><h2 style="flex:1">${escape(title)}</h2><button type="button" id="closeModal" aria-label="Close">✕</button></div><form id="dialogForm">${fields}</form>`;
 button('closeModal',()=>d.close());if(action)form('dialogForm',action);d.showModal();
}
function settings(){modal('Account',`<p class="notice">${escape(pb.authStore.record.email||'')}</p><button type="button" id="emailChange" class="primary">Change email</button><button type="button" class="link" id="notify">Enable browser notifications</button><button type="button" id="logout" class="link danger">Sign out</button>`);button('logout',()=>signout());button('notify',push);button('emailChange',()=>modal('Change your email',field('New email address','newEmail','email')+submit('Send confirmation link'),async()=>{await col('users').requestEmailChange(value('newEmail'));$('modal').close();toast('Confirmation link sent to your new email.')}))}
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
 const peer=people[r.conversation]||'Your contact';
 if(r.status==='ringing'){
  const incoming=r.receiver===usr();
  renderCallId=null;
  if(host.dataset.phase!==r.id){
   host.dataset.phase=r.id;host.classList.remove('min');
   host.innerHTML=`<div class="callhead"><strong>${incoming?'Incoming':'Calling'} · ${escape(peer)}</strong><span>${escape(r.kind)} call</span></div><div class="call-controls">${incoming?'<button id="accept">Accept</button><button id="decline" class="hang">Decline</button>':'<button id="decline" class="hang">Cancel</button>'}</div>`;
   button('accept',async()=>{const item=await api('/calls/'+r.id+'/accept','POST',{deviceId:device});await applyCall(item)});
   button('decline',async()=>{await applyCall(await api('/calls/'+r.id+(incoming?'/reject':'/end'),'POST',{}))});
   if(incoming)playChime();
  }return;
 }
 if(renderCallId===r.id)return;
 renderCallId=r.id;host.dataset.phase='';host.classList.remove('min');manualMode='auto';
 host.innerHTML=`<div class="callhead"><strong>${escape(peer)}</strong><span id="callState">Connecting…</span><button id="collapse" aria-label="Toggle compact call">−</button></div><div class="stage"><div id="remote"></div><div id="local"></div><form id="incallForm" class="incall" hidden><div id="recent"></div><div class="row" style="flex-wrap:nowrap"><input id="incallText" placeholder="Message during call" aria-label="Message during call"><button type="submit">Send</button></div></form></div><div id="audioMount"></div><div class="call-controls"><button id="mute">Mic on</button><button id="camera">Camera</button><button id="flip">Flip</button><button id="share">Share screen</button><button id="inChat">Chat</button><select aria-label="Call quality" id="quality"><option value="auto">Auto HD</option><option value="smooth">Smooth</option><option value="audio">Audio only</option></select><button class="hang" id="hang">End call</button></div>`;
 button('collapse',()=>{host.classList.toggle('min');if(room)for(const p of room.remoteParticipants.values())for(const t of p.videoTrackPublications.values())t.setVideoQuality(host.classList.contains('min')?VideoQuality.LOW:VideoQuality.HIGH)});
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
 if($('mute'))$('mute').textContent=active.localParticipant.isMicrophoneEnabled?'Mic on':'Mic off';
 if(restore?.camera??(record.kind==='video'))await active.localParticipant.setCameraEnabled(true).catch(()=>toast('Camera unavailable. Audio stays connected.'));
 if(manualMode!=='auto'){if($('quality'))$('quality').value=manualMode;await applyQuality(manualMode)}
 button('mute',async()=>{await active.localParticipant.setMicrophoneEnabled(!active.localParticipant.isMicrophoneEnabled);$('mute').textContent=active.localParticipant.isMicrophoneEnabled?'Mic on':'Mic off'});
 button('camera',async()=>{if(manualMode==='audio'){$('quality').value=manualMode='auto'}await active.localParticipant.setCameraEnabled(!active.localParticipant.isCameraEnabled)});
 let facing='user';
 button('flip',async()=>{const pub=active.localParticipant.getTrackPublication(Track.Source.Camera),t=pub?.track;if(t){facing=facing==='user'?'environment':'user';await t.restartTrack({facingMode:facing})}});
 button('share',async()=>{await active.localParticipant.setScreenShareEnabled(!active.localParticipant.isScreenShareEnabled)});
 $('quality').onchange=()=>{manualMode=value('quality');applyQuality(manualMode).catch(e=>toast(reason(e)))};
}
async function applyQuality(mode){
 if(!room)return;
 const p=room.localParticipant;
 if(mode==='audio'){await p.setCameraEnabled(false)}else{
  const track=p.getTrackPublication(Track.Source.Camera)?.track;
  if(track)await track.restartTrack({resolution:(mode==='smooth'?VideoPresets.h360:VideoPresets.h720).resolution,frameRate:mode==='smooth'?15:30});
 }
 for(const other of room.remoteParticipants.values())for(const pub of other.videoTrackPublications.values()){
  pub.setSubscribed(mode!=='audio');if(mode!=='audio')pub.setVideoQuality(mode==='smooth'?VideoQuality.LOW:VideoQuality.HIGH);
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
pb.realtime.subscribe('PB_CONNECT',()=>{if(conversation)sync().catch(()=>{})}).catch(()=>{});
navigator.serviceWorker?.addEventListener('message',()=>{if(usr()){refreshCall().catch(()=>{});sync().catch(()=>{})}});
init().catch(e=>{gate();toast(reason(e))});

