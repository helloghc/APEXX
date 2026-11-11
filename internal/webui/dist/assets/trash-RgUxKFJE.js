import{aa as l,ab as d,ac as x,ad as y,ae as b,c as f}from"./index-CyUTbvDi.js";var m=class extends l{constructor(e,t){super(e,t)}bindMethods(){super.bindMethods(),this.fetchNextPage=this.fetchNextPage.bind(this),this.fetchPreviousPage=this.fetchPreviousPage.bind(this)}setOptions(e){super.setOptions({...e,behavior:d()})}getOptimisticResult(e){return e.behavior=d(),super.getOptimisticResult(e)}fetchNextPage(e){return this.fetch({...e,meta:{fetchMore:{direction:"forward"}}})}fetchPreviousPage(e){return this.fetch({...e,meta:{fetchMore:{direction:"backward"}}})}createResult(e,t){const{state:a}=e,r=super.createResult(e,t),{isFetching:i,isRefetching:v,isError:c,isRefetchError:p}=r,s=a.fetchMeta?.fetchMore?.direction,h=c&&s==="forward",o=i&&s==="forward",n=c&&s==="backward",u=i&&s==="backward";return{...r,fetchNextPage:this.fetchNextPage,fetchPreviousPage:this.fetchPreviousPage,hasNextPage:y(t,a.data),hasPreviousPage:x(t,a.data),isFetchNextPageError:h,isFetchingNextPage:o,isFetchPreviousPageError:n,isFetchingPreviousPage:u,isRefetchError:p&&!h&&!n,isRefetching:v&&!o&&!u}}};function w(e,t){return b(e,m)}/**
 * @license lucide-react v1.71.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const P={name:"arrow-left",size:24,node:[["path",{d:"m12 19-7-7 7-7",key:"1l729n"}],["path",{d:"M19 12H5",key:"x3x0zl"}]]};P.node;const N=f(P);/**
 * @license lucide-react v1.71.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const g={name:"trash",size:24,node:[["path",{d:"M10 11v6",key:"nco0om"}],["path",{d:"M14 11v6",key:"outv1u"}],["path",{d:"M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6",key:"miytrc"}],["path",{d:"M3 6h18",key:"d0wm0j"}],["path",{d:"M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2",key:"e791ji"}]],aliases:["trash-2"]};g.node;const R=f(g);export{N as A,R as T,w as u};
