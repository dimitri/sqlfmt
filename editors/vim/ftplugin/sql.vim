" sql.vim -- wire the sqlfmt formatter into Vim's format operators
"
" Install by adding this repository's editors/vim directory to your
" 'runtimepath', e.g. in ~/.vimrc:
"
"   set runtimepath+=~/dev/PostgreSQL/sqlfmt/editors/vim
"
" or by copying/symlinking this file directly to ~/.vim/ftplugin/sql.vim.
"
" sqlfmt (https://github.com/dimitri/sqlfmt) reads SQL on stdin and
" writes formatted SQL to stdout when given no arguments -- exactly
" what Vim's 'formatprg'/'equalprg' options expect: they pipe the
" affected lines through the given external command and replace them
" with its output. No plugin logic is needed beyond setting them:
"
"   gqip / gqap / gqG    -- reformat a paragraph/block/the whole buffer
"                            via 'formatprg' (the gq operator)
"   =ap / =G / gg=G       -- same, via 'equalprg' (the = operator)
"   :%!sqlfmt              -- reformat the whole buffer directly

if exists('b:did_sqlfmt_ftplugin')
  finish
endif
let b:did_sqlfmt_ftplugin = 1

if !exists('g:sqlfmt_command')
  let g:sqlfmt_command = 'sqlfmt'
endif

let &l:formatprg = g:sqlfmt_command
let &l:equalprg = g:sqlfmt_command

let b:undo_ftplugin = (exists('b:undo_ftplugin') ? b:undo_ftplugin . ' | ' : '')
      \ . 'setlocal formatprg< equalprg<'
