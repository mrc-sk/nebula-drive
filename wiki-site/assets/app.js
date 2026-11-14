
let DATA=[];
fetch('search.json').then(r=>r.json()).then(d=>{DATA=d}).catch(()=>{});
const box=document.getElementById('searchResults'), input=document.getElementById('search');
input.addEventListener('input',()=>{
  const q=input.value.trim().toLowerCase();
  if(!q){box.innerHTML='';return;}
  const hits=DATA.filter(x=>(x.title+' '+x.cat+' '+x.text).toLowerCase().includes(q)).slice(0,12);
  box.innerHTML=hits.length?hits.map(x=>'<a href="pages/'+x.slug+'.html"><span class="sr-cat">'+x.cat+'</span> · '+x.title+'</a>').join(''):'<div style="color:#6f7c93;padding:6px 10px">无匹配结果</div>';
});
