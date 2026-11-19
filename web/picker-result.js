// Normalize SDK variants without mistaking display labels for filesystem paths.
export function pickerPath(result,depth=0){
 if(depth>10)throw Error('目录返回结构嵌套过深');
 if(result==null)return null;
 if(Array.isArray(result))return pickerPath(result[0],depth+1);
 if(typeof result==='string'){
 if(result.startsWith('/'))return result;
 const raw=result.trim();if(raw.startsWith('{')||raw.startsWith('[')||raw.startsWith('\"')){try{return pickerPath(JSON.parse(raw),depth+1)}catch(e){if(e instanceof SyntaxError)return null;throw e}}
 return null;
 }
 if(typeof result!=='object')return null;
 if('code' in result && result.code!==0 && result.code!=='0')throw Error(result.msg||'目录选择未获授权');
 for(const key of ['data','paths','path','selectedPaths','files']){
  if(key in result){const p=pickerPath(result[key],depth+1);if(p)return p;}
 }
 return null;
}
