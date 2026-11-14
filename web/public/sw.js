self.addEventListener('push', event => {
  const p=event.data?.json();
  if(!p || Date.parse(p.expiresAt)<=Date.now()) return;
  const call=p.event==='incoming_call';
  event.waitUntil(self.registration.showNotification(call?'Incoming call':'New message',{
    body:'Open Apun Ka Communication',tag:p.event+':'+p.id,icon:'/mark.svg',data:p,
  }));
});
self.addEventListener('notificationclick', event=>{
  event.notification.close();
  event.waitUntil((async()=>{
    const windows=await clients.matchAll({type:'window',includeUncontrolled:true});
    if(windows[0]){await windows[0].focus();windows[0].postMessage('sync');}
    else await clients.openWindow('/');
  })());
});
