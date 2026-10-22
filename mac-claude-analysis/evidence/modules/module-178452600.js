// @bun @bytecode
// Claude Code is a Beta product per Anthropic's Commercial Terms of Service.
// By using Claude Code, you agree that all code acceptance or rejection decisions you make,
// and the associated conversations in context, constitute Feedback under Anthropic's Commercial Terms,
// and may be used to improve Anthropic's products, including training models.
// You are responsible for reviewing any code suggestions before use.

// (c) Anthropic PBC. All rights reserved. Use is subject to the Legal Agreements outlined here: https://code.claude.com/docs/en/legal-and-compliance.

// Version: 2.1.284
import{po}from"/$bunfs/root/chunk-t7btwkxc.js";import{a}from"/$bunfs/root/chunk-s58fgq12.js";import{lM}from"/$bunfs/root/chunk-c44rdggd.js";import{qn}from"/$bunfs/root/chunk-8x984w3d.js";function u(){if(a.CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST)return!1;return!qn()}function sUo(){return a.CLAUDE_CODE_ENVIRONMENT_KIND==="byoc"&&!a.CLAUDE_CODE_BYOC_ENABLE_DATADOG}function l(){return a.CLAUDE_CODE_CUSTOM_OAUTH_URL!==void 0}function Kg(){return u()||po()!==null||lM()||l()}function iUo(){return u()||po()!==null||l()}function F5(){return a.CLAUDE_CODE_ENABLE_FEEDBACK_SURVEY_FOR_OTEL}function aN(){if(F5())return!1;return lM()}import{sep as f}from"path";var lUt=new Set([".git","hooks",".husky",".githooks","node_modules",".vscode",".idea","head","config","objects","refs",".claude","skills","commands","agents",".cargo",".devcontainer",".yarn",".mvn"]),cUt=/[\u200c-\u200f\u202a-\u202e\u206a-\u206f\ufeff]/,E=new RegExp(cUt,"g");function Ji(e){return c(e,!0)}function aUo(e){return c(e,!1)}function c(e,o){let n=e.toLowerCase().replace(/\u0131/g,"i").replace(/\u017f/g,"s"),t=n.replace(E,"");return(o?t.replace(/:.*$/,""):t).replace(/[. ]+$/,"")||n}function Y2r(e){return!1}function v3(e,o,n){let t=e.slice(o.length).split(f),i=t.length-1;for(let r=0;r<t.length;r++){let s=Ji(t[r]);if(lUt.has(s)||Y2r(s))return!0;if(r===i&&n?.has(s))return!0}return!1}
export{sUo,Kg,iUo,F5,aN,lUt,cUt,Ji,aUo,Y2r,v3};
