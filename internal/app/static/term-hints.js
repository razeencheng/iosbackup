(function(global){
 'use strict';var doc=global.document,owner=null,popup=null,timer=null,pinned=false,seq=0;
 function close(){if(timer)global.clearTimeout(timer);timer=null;if(owner)owner.removeAttribute('aria-describedby');if(popup)popup.remove();owner=null;popup=null;pinned=false;}
 function place(){if(!owner||!popup)return;var a=owner.getBoundingClientRect(),b=popup.getBoundingClientRect(),gap=10;popup.style.left=Math.max(gap,Math.min(a.left,global.innerWidth-b.width-gap))+'px';popup.style.top=Math.max(gap,Math.min(a.bottom+8,global.innerHeight-b.height-gap))+'px';}
 function open(button,keep){if(timer)global.clearTimeout(timer);if(owner===button){pinned=pinned||keep;return;}close();owner=button;pinned=keep;popup=doc.createElement('div');popup.className='term-tooltip';popup.id='term-tooltip-'+(++seq);popup.setAttribute('role','tooltip');popup.textContent=global.IOSBKI18n.t('term.'+button.getAttribute('data-term')+'.hint');var container=button.closest('dialog[open]')||doc.body;container.appendChild(popup);button.setAttribute('aria-describedby',popup.id);place();}
 function trigger(target){return target.closest?target.closest('[data-term]'):null;}
 function later(){if(pinned)return;if(timer)global.clearTimeout(timer);timer=global.setTimeout(close,250);}
 doc.addEventListener('mouseover',function(e){var b=trigger(e.target);if(b)open(b,false);else if(popup&&popup.contains(e.target)){if(timer)global.clearTimeout(timer);}});
 doc.addEventListener('mouseout',function(e){if(owner&&(owner.contains(e.target)||popup.contains(e.target)))later();});
 doc.addEventListener('focusin',function(e){var b=trigger(e.target);if(b)open(b,false);});
 doc.addEventListener('focusout',function(){later();});
 doc.addEventListener('click',function(e){var b=trigger(e.target);if(b){e.stopPropagation();if(owner===b&&pinned)close();else open(b,true);}else if(!popup||!popup.contains(e.target))close();});
 doc.addEventListener('keydown',function(e){if(e.key==='Escape'&&owner){if(e.preventDefault)e.preventDefault();if(e.stopPropagation)e.stopPropagation();close();}});
 global.addEventListener('resize',place);global.addEventListener('scroll',place,true);global.addEventListener('iosbk:localechange',function(){close();if(doc.querySelectorAll)doc.querySelectorAll('[data-term]').forEach(function(b){b.setAttribute('aria-label',global.IOSBKI18n.t('history.explain',{term:global.IOSBKI18n.t('term.'+b.getAttribute('data-term')+'.title')}));});});
})(window);
