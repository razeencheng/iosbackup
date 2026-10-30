'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const listeners={};const nodes=[];let active;
function node(){return {style:{},attrs:{},classList:{add(){},remove(){}},setAttribute(k,v){this.attrs[k]=v},getAttribute(k){return this.attrs[k]},removeAttribute(k){delete this.attrs[k]},contains(n){return n===this},getBoundingClientRect(){return {left:375,right:390,top:100,bottom:130,width:320,height:100}},closest(selector){return selector==='[data-term]'&&this.attrs['data-term']?this:null},focus(){active=this},remove(){this.removed=true}}}
const doc={body:{appendChild(n){nodes.push(n)}},createElement:node,addEventListener(k,f){listeners[k]=f},get activeElement(){return active}};
const winListeners={};const window={document:doc,innerWidth:390,innerHeight:667,addEventListener(k,f){winListeners[k]=f},setTimeout,clearTimeout,IOSBKI18n:{t(k){return k}}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/term-hints.js'),'utf8'),{window});
const b=node();b.setAttribute('data-term','pinned');active=b;listeners.focusin({target:b});assert.ok(b.attrs['aria-describedby']);assert.equal(nodes.at(-1).attrs.role,'tooltip');assert.ok(parseFloat(nodes.at(-1).style.left)<=62,'keep tooltip inside narrow viewport');
listeners.click({target:b,stopPropagation(){}});listeners.keydown({key:'Escape'});assert.equal(b.attrs['aria-describedby'],undefined,'Escape clears relationship');
listeners.click({target:b,stopPropagation(){}});assert.ok(b.attrs['aria-describedby'],'touch/click opens');listeners.click({target:node(),stopPropagation(){}});assert.equal(b.attrs['aria-describedby'],undefined);
console.log('term hints support focus, click, Escape, outside dismissal and viewport boundaries');
