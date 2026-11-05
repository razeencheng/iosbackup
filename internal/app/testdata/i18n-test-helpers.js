'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const runtime = path.join(root, 'static/i18n.js');
const catalogs=JSON.parse(fs.readFileSync(path.join(root,'static/ui-messages.json'),'utf8'));
const apiCatalog=JSON.parse(fs.readFileSync(path.join(root,'../i18n/messages.json'),'utf8'));
// 仅模拟浏览器边界；页面状态和翻译执行生产代码。
function boot({stored={},cookie='',languages=['en-US'],storageThrows=false,storageReadThrows=false,storageWriteThrows=false,cookieReadThrows=false,cookieWriteThrows=false,timezone=''}={}) {
  const values={...stored}, events=[], requests=[], listeners={};
  const document={documentElement:{lang:'',dataset:{timezone},setAttribute(k,v){this[k]=v;}},querySelectorAll(){return [];}};
  let cookieValue=cookie; const storageWrites=[],cookieWrites=[];
  Object.defineProperty(document,'cookie',{get(){if(cookieReadThrows)throw Error('cookie blocked');return cookieValue;},set(value){cookieWrites.push(value);if(cookieWriteThrows)throw Error('cookie blocked');cookieValue=value;}});
  const window={document,navigator:{languages,language:languages[0]||''},localStorage:{getItem(k){if(storageThrows||storageReadThrows)throw Error('blocked');return values[k]||null;},setItem(k,v){storageWrites.push({key:k,value:v});if(storageThrows||storageWriteThrows)throw Error('blocked');values[k]=v;},removeItem(k){delete values[k];}},dispatchEvent(e){events.push(e);(listeners[e.type]||[]).forEach(fn=>fn(e));},addEventListener(type,fn){(listeners[type]||=[]).push(fn);},fetch(url,opts){requests.push({url,opts});return Promise.resolve({});}};
  const context=vm.createContext({...window,window,CustomEvent:class{constructor(type,opts){this.type=type;this.detail=opts.detail;}},Intl,Date,Headers});
  vm.runInContext(fs.readFileSync(runtime,'utf8'),context);
  context.IOSBKI18n=window.IOSBKI18n;
  return {api:window.IOSBKI18n,values,document,events,requests,context,window,storageWrites,cookieWrites};
}
function dom(app) {
 const nodes=new Map();
 function node(id='') {
  const attributes={}, children={}, handlers={}, classes=new Set();
  const result={id,value:'',type:'password',textContent:'',innerHTML:'',hidden:false,style:{},dataset:{},attributes,children,handlers,
   classList:{toggle(k,v){if(v===undefined)v=!classes.has(k);v?classes.add(k):classes.delete(k);},add(k){classes.add(k);},remove(k){classes.delete(k);},contains(k){return classes.has(k);}},
   setAttribute(k,v){attributes[k]=String(v);},getAttribute(k){return attributes[k]??null;},removeAttribute(k){delete attributes[k];},
   querySelector(k){return children[k]||(children[k]=node());},querySelectorAll(){return [];},insertAdjacentHTML(){},appendChild(child){(children.list||=[]).push(child);},addEventListener(type,fn){handlers[type]=fn;},focus(){},getClientRects(){return [1];},replaceChildren(){children.list=[];}};
  return result;
 }
 app.document.getElementById=id=>nodes.get(id)||(nodes.set(id,node(id)),nodes.get(id));
 app.document.querySelector=()=>null;
 app.document.querySelectorAll=selector=>{
  const match=selector.match(/^\[([^\]]+)\]$/);
  return match?[...nodes.values()].filter(n=>Object.hasOwn(n.attributes,match[1])):[];
 };
 app.document.addEventListener=()=>{};
 app.document.createElement=()=>node();
 app.document.body=node('body');app.document.body.dataset={};
 Object.assign(app.context,{setTimeout:()=>1,clearTimeout(){},AbortController,console});
 app.window.setTimeout=()=>1;app.window.clearTimeout=()=>{};
 app.api.register(catalogs);
 return {get:app.document.getElementById,nodes,node};
}
function pageScript(name) {
 const html=fs.readFileSync(path.join(root,'templates',name),'utf8');
 const scripts=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
 return (name==='index.html'?fs.readFileSync(path.join(root,'static/backup-browser.js'),'utf8')+'\n'+fs.readFileSync(path.join(root,'static/backup-console.js'),'utf8')+'\nvar IOSBKConsole=window.IOSBKConsole;\n':'')+scripts.at(-1)[1].replace('{{.InitialStatus}}','{"devices":[]}');
}

module.exports={boot,dom,pageScript,catalogs,apiCatalog,root};
