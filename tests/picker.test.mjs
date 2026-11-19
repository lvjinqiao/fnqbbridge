import {pickerPath} from '../web/picker-result.js';
import assert from 'node:assert/strict';
for(const value of [['/vol1/1000/目录'],{code:0,data:['/vol1/1000/目录']},{code:0,data:{paths:['/vol1/1000/目录']}},{code:0,data:[{path:'/vol1/1000/目录'}]}])assert.equal(pickerPath(value),'/vol1/1000/目录');
assert.equal(pickerPath(null),null);assert.equal(pickerPath({code:0,data:{name:'目录'}}),null);assert.throws(()=>pickerPath({code:403,msg:'权限不足'}));console.log('PASS picker return shapes and cancellation');
assert.equal(pickerPath(JSON.stringify({code:0,data:{paths:['/vol2/1000/手机 目录']}})),'/vol2/1000/手机 目录');
assert.equal(pickerPath({code:'0',data:JSON.stringify(['/vol2/1000/手机 目录'])}),'/vol2/1000/手机 目录');
assert.equal(pickerPath('not a path'),null);
console.log('PASS serialized native bridge responses');
