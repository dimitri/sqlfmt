;;; sqlfmt.el --- Format PostgreSQL SQL with sqlfmt -*- lexical-binding: t; -*-

;; Copyright (c) 2026, Dimitri Fontaine

;; Author: Dimitri Fontaine
;; Keywords: languages, sql, tools
;; URL: https://github.com/dimitri/sqlfmt
;; Package-Requires: ((emacs "26.1"))
;; Version: 0.1.0

;; This file is not part of GNU Emacs.

;; SPDX-License-Identifier: PostgreSQL
;; See the LICENSE file at the root of https://github.com/dimitri/sqlfmt
;; for the full PostgreSQL Licence text this file is distributed under.

;;; Commentary:

;; Minor mode wiring the `sqlfmt' command-line formatter
;; (https://github.com/dimitri/sqlfmt) into `sql-mode' buffers.
;;
;; Enable it with:
;;
;;   (add-hook 'sql-mode-hook #'sqlfmt-mode)
;;
;; `sqlfmt-mode' does not define any new keybindings.  Instead it plugs
;; `sqlfmt' into Emacs's own generic statement-navigation and
;; region-indent machinery, so existing muscle memory keeps working:
;;
;; - `beginning-of-defun-function' / `end-of-defun-function' are set to
;;   SQL statement boundaries (a top-level `;', skipping over string
;;   and comment syntax), so `mark-defun' -- already bound to `C-M-h'
;;   everywhere, exactly as in `python-mode' -- selects the statement
;;   at point.
;; - `indent-region-function' is set to reformat the selected region
;;   through `sqlfmt'.  `indent-for-tab-command' (`TAB') already calls
;;   `indent-region' whenever the region is active, so `C-M-h TAB'
;;   selects the statement at point and reformats it in house style.
;;
;; `sqlfmt-buffer' and `sqlfmt-region' are also available as ordinary
;; interactive commands, and `sqlfmt-before-save-hook' can be added to
;; `before-save-hook' to format on save:
;;
;;   (add-hook 'sql-mode-hook #'sqlfmt-mode)
;;   (add-hook 'sql-mode-hook
;;             (lambda () (add-hook 'before-save-hook
;;                                  #'sqlfmt-before-save-hook nil t)))

;;; Code:

(defgroup sqlfmt nil
  "Format SQL with the external `sqlfmt' command-line tool."
  :group 'sql
  :link '(url-link "https://github.com/dimitri/sqlfmt"))

(defcustom sqlfmt-command "sqlfmt"
  "Name or path of the `sqlfmt' executable."
  :type 'string
  :group 'sqlfmt)

(defcustom sqlfmt-show-errors t
  "When non-nil, show `sqlfmt's stderr in *sqlfmt-errors* on failure."
  :type 'boolean
  :group 'sqlfmt)

;;; Statement (top-level ";") boundaries, for beginning/end-of-defun.

(defun sqlfmt--in-string-or-comment-p (pos)
  "Non-nil if POS is inside a string or comment per the syntax table.
Wrapped in `save-excursion': `sql-mode's `syntax-propertize-function'
(for `''`-escaped strings) does not reliably preserve point, and
`syntax-ppss' calls it as needed, so an unguarded call here can quietly
move point out from under statement-boundary scanning callers."
  (save-excursion
    (let ((state (syntax-ppss pos)))
      (or (nth 3 state) (nth 4 state)))))

(defun sqlfmt--statement-terminator-at-p (pos)
  "Non-nil if the character just before POS is a real statement `;'."
  (and (> pos (point-min))
       (eq (char-after (1- pos)) ?\;)
       (not (sqlfmt--in-string-or-comment-p (1- pos)))))

(defun sqlfmt-beginning-of-statement (&optional n)
  "`beginning-of-defun-function' for SQL statements.
Moves point to the start of the current top-level statement, or the
Nth previous one.  Statements are delimited by a `;' outside of any
string or comment.  Intended for `beginning-of-defun'/`mark-defun'."
  (let ((n (or n 1)))
    (dotimes (_ (max n 1))
      ;; Step left of a semicolon point is already sitting right after,
      ;; so repeated calls walk to the previous statement, not stay put.
      (when (sqlfmt--statement-terminator-at-p (point))
        (backward-char))
      (let ((found nil))
        (while (and (not found) (re-search-backward ";" nil t))
          (unless (sqlfmt--in-string-or-comment-p (point))
            (setq found t)))
        (goto-char (if found (1+ (point)) (point-min))))
      (skip-chars-forward " \t\n\r")))
  t)

(defun sqlfmt-end-of-statement ()
  "`end-of-defun-function' for SQL statements.
Moves point just past the terminating `;' of the current top-level
statement, or to `point-max' if the statement is unterminated."
  (let ((found nil))
    (while (and (not found) (re-search-forward ";" nil t))
      (unless (sqlfmt--in-string-or-comment-p (1- (point)))
        (setq found t)))
    (unless found (goto-char (point-max)))))

;;; Running sqlfmt.

(defun sqlfmt--replace-region (beg end)
  "Replace BEG..END with the `sqlfmt' output for that text.
Applies the result with `replace-buffer-contents', so only the spans
that actually changed are touched -- point, markers and overlays
elsewhere in the buffer are left alone.  Signals an error and leaves
the buffer untouched if `sqlfmt' exits non-zero."
  (let ((input (buffer-substring-no-properties beg end))
        (out-buf (generate-new-buffer " *sqlfmt-output*"))
        ;; call-process-region's (REAL-BUFFER STDERR-FILE) BUFFER form
        ;; wants a *file name* for stderr, unlike call-process, which
        ;; also accepts a buffer there.
        (err-file (make-temp-file "sqlfmt-stderr")))
    (unwind-protect
        (let ((status (with-temp-buffer
                         (insert input)
                         (call-process-region (point-min) (point-max)
                                               sqlfmt-command
                                               nil (list out-buf err-file) nil))))
          (if (not (eq status 0))
              (progn
                (when sqlfmt-show-errors
                  (with-current-buffer (get-buffer-create "*sqlfmt-errors*")
                    (erase-buffer)
                    (insert-file-contents err-file)
                    (display-buffer (current-buffer))))
                (error "sqlfmt failed (%s), buffer left unchanged" status))
            (save-restriction
              (narrow-to-region beg end)
              (goto-char (point-min))
              (replace-buffer-contents out-buf))))
      (kill-buffer out-buf)
      (delete-file err-file))))

(defun sqlfmt-region (beg end)
  "Reformat the region BEG..END with `sqlfmt'."
  (interactive "r")
  (sqlfmt--replace-region beg end))

(defun sqlfmt-buffer ()
  "Reformat the current buffer with `sqlfmt'."
  (interactive)
  (sqlfmt--replace-region (point-min) (point-max)))

(defun sqlfmt-indent-region (beg end)
  "`indent-region-function' for `sqlfmt-mode': reformat BEG..END.
This is what makes `TAB' on an active region -- e.g. one just marked
with `C-M-h' -- reformat it via `sqlfmt' instead of re-indenting
line-by-line, since `indent-for-tab-command' calls `indent-region'
whenever the region is active, and `indent-region' defers to
`indent-region-function' when it is set."
  (sqlfmt--replace-region beg end))

(defun sqlfmt-mark-statement (&optional _arg)
  "Put point at the beginning of the SQL statement at point, mark at its end.
A drop-in replacement for `mark-defun' in `sqlfmt-mode' buffers.
Emacs's generic `mark-defun'/`end-of-defun' machinery layers in extra
positional bookkeeping (repeat-count handling, \"were we between two
defuns\" retries, trailing-comment skipping...) tuned for paren- or
brace-delimited defuns; it does not compose cleanly with a simple
`;'-delimited statement boundary, so this reimplements the one
behavior actually wanted -- select the enclosing statement -- directly
in terms of `sqlfmt-beginning-of-statement'/`sqlfmt-end-of-statement'."
  (interactive "p")
  (push-mark (point))
  (sqlfmt-beginning-of-statement)
  (let ((beg (point)))
    (sqlfmt-end-of-statement)
    (push-mark (point) nil t)
    (goto-char beg)))

(defvar sqlfmt-mode-map
  (let ((map (make-sparse-keymap)))
    (define-key map [remap mark-defun] #'sqlfmt-mark-statement)
    map)
  "Keymap for `sqlfmt-mode'.
Remaps `mark-defun' (`C-M-h' by default, as in `python-mode') to
`sqlfmt-mark-statement' rather than introducing a new binding.")

;;;###autoload
(define-minor-mode sqlfmt-mode
  "Minor mode wiring the `sqlfmt' formatter into SQL statement
navigation and region indentation.

With `sqlfmt-mode' enabled, `C-M-h' (`mark-defun', remapped to
`sqlfmt-mark-statement') selects the SQL statement at point, and `TAB'
on the resulting active region reformats it via `sqlfmt' (through
`indent-region-function', which `indent-for-tab-command' already
consults whenever the region is active)."
  :lighter " Sqlfmt"
  :keymap sqlfmt-mode-map
  (if sqlfmt-mode
      (progn
        (setq-local beginning-of-defun-function #'sqlfmt-beginning-of-statement)
        (setq-local end-of-defun-function #'sqlfmt-end-of-statement)
        (setq-local indent-region-function #'sqlfmt-indent-region))
    (kill-local-variable 'beginning-of-defun-function)
    (kill-local-variable 'end-of-defun-function)
    (kill-local-variable 'indent-region-function)))

;;;###autoload
(defun sqlfmt-before-save-hook ()
  "Reformat the buffer with `sqlfmt' before saving, if `sqlfmt-mode' is on.
Add to `before-save-hook' buffer-locally to format on save.  Errors
are reported but do not block the save."
  (when sqlfmt-mode
    (condition-case err
        (sqlfmt-buffer)
      (error (message "%s" (error-message-string err))))))

(provide 'sqlfmt)

;;; sqlfmt.el ends here
