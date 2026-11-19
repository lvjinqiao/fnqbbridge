import { pickerPath } from './picker-result.js';
import { TrimApp } from './trim-sdk.js';
const sdk = new TrimApp();
const timeout=(p,ms=15000)=>Promise.race([p,new Promise((_,reject)=>setTimeout(()=>reject(Error('飞牛目录选择器未响应，请在飞牛桌面内打开应用重试')),ms))]);
window.pickFnosDirectory=async()=>{
 await timeout(sdk.ready(),5000);
 if(sdk.isStandaloneWeb)throw Error('请从飞牛桌面图标打开应用，以使用系统目录选择器；当前可手动填写目录。');
 // Native picker stays open until the user confirms or cancels; never race user input against a timer.
 const result=await sdk.pickUserFile({directory:true,sidebarGroup:['myFiles','team','storage','external','favorites']});
 if(!result)return null;
 const path=pickerPath(result);
 if(!path)throw Error('目录选择结果没有可用的绝对路径，请手动填写目录');
 return path;
};
