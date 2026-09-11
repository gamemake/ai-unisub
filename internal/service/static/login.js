(function(){
  'use strict';
  var form=document.getElementById('loginForm');
  var button=document.getElementById('loginButton');
  var error=document.getElementById('loginError');
  function errorText(message){
    var messages={
      'username and password are required':'请输入用户名和密码',
      'could not authenticate user':'无法验证用户',
      'invalid username or password':'用户名或密码错误',
      'network request failed':'网络请求失败'
    };
    return messages[message]||message||'请求失败';
  }
  async function login(event){
    event.preventDefault();
    button.disabled=true;
    error.textContent='';
    try{
      var response=await fetch('/login',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:document.getElementById('username').value.trim(),password:document.getElementById('password').value})});
      var body;
      try{body=await response.json()}catch(e){throw new Error('invalid server response')}
      if(!response.ok){var message=typeof body.error==='string'?body.error:(body.error&&body.error.message);throw new Error(errorText(message||response.statusText))}
      window.location.assign('/home');
    }catch(e){error.textContent=errorText(e.message)}finally{button.disabled=false}
  }
  form.addEventListener('submit',login);
})();
