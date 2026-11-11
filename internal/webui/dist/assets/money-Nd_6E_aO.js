import{c as s}from"./index-qXSxMtKs.js";/**
 * @license lucide-react v1.71.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const c={name:"search",size:24,node:[["path",{d:"m21 21-4.34-4.34",key:"14j7rj"}],["circle",{cx:"11",cy:"11",r:"8",key:"4ej97u"}]]};c.node;const l=s(c);function i(t){let e=t.trim().replace(/^\$/,"").replace(/\s/g,"");if(e===""||(/,\d{1,2}$/.test(e)&&!e.includes(".")&&(e=e.replace(/,(\d{1,2})$/,".$1")),e=e.replace(/,/g,""),!/^\d+(\.\d{0,2})?$/.test(e)))return null;const[r="0",n=""]=e.split("."),a=Number(r)*100+Number((n+"00").slice(0,2));return Number.isSafeInteger(a)?a:null}function u(t){const e=t<0,r=Math.abs(Math.trunc(t));return`${e?"-":""}${Math.floor(r/100)}.${String(r%100).padStart(2,"0")}`}function d(t,e="MXN"){const r=t<0,n=Math.abs(Math.trunc(t)),a=String(Math.floor(n/100)).replace(/\B(?=(\d{3})+(?!\d))/g,",");return`${r?"-":""}$${a}.${String(n%100).padStart(2,"0")} ${e}`}export{l as S,u as c,d as f,i as p};
