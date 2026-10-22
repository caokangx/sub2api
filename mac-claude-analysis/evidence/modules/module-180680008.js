// @bun @bytecode
// Claude Code is a Beta product per Anthropic's Commercial Terms of Service.
// By using Claude Code, you agree that all code acceptance or rejection decisions you make,
// and the associated conversations in context, constitute Feedback under Anthropic's Commercial Terms,
// and may be used to improve Anthropic's products, including training models.
// You are responsible for reviewing any code suggestions before use.

// (c) Anthropic PBC. All rights reserved. Use is subject to the Legal Agreements outlined here: https://code.claude.com/docs/en/legal-and-compliance.

// Version: 2.1.284
import{t}from"/$bunfs/root/chunk-kwj9cfj4.js";import{pht,f3r}from"/$bunfs/root/chunk-j040ssqp.js";import{omn,VNo,st,D5n,b1t,zft,l9n,Vft,zE,x}from"/$bunfs/root/chunk-f5tnbmwk.js";import{Xut}from"/$bunfs/root/chunk-667544cv.js";var u="tengu_log_datadog_events";function s(){if(zft("datadog"))return!1;try{return x(u,!1)}catch{return!1}}function f(e){try{omn(st())}catch{}return VNo(e)}function d(e,n,a){let o=f(n),r=a!==null?{...o,sample_rate:a}:o;if(s())Xut(e,pht(r));Vft(e,r)}var i=!1;function m(e,n){if(i){t(`logEvent reentered while collecting metadata \u2014 dropped ${e}. A getEventMetadata dependency (model/betas/auth) called logEvent synchronously; defer it (queueMicrotask) or move it out of the metadata path.`,{level:"error"});return}i=!0;try{let a=l9n(e);if(a===0)return;if(D5n()){d(e,n,a);return}let o=()=>{i=!0;try{d(e,n,a)}finally{i=!1}};b1t().then(o,o)}finally{i=!1}}async function c(e,n){let a=l9n(e);if(a===0)return;if(!D5n())await b1t();let o=f(n),r=a!==null?{...o,sample_rate:a}:o,l=[];if(s())l.push(Xut(e,pht(r)));l.push(zE(e,r)),await Promise.all(l)}function fU(){f3r({logEvent:m,logEventAsync:c})}
export{fU};
