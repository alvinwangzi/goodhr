/** 本文件负责邮件编辑器组件，使用 next/dynamic 避免 WangEditor Toolbar 重复创建问题。 */
"use client";

import { Box } from "@mui/material";
import type { IDomEditor, IEditorConfig, IToolbarConfig } from "@wangeditor/editor";
import "@wangeditor/editor/dist/css/style.css";
import { Editor, Toolbar } from "@wangeditor/editor-for-react";
import { useEffect, useRef, useState } from "react";

type MailEditorProps = {
  value: string;
  onChange: (html: string) => void;
  toolbarConfig?: Partial<IToolbarConfig>;
  editorConfig?: Partial<IEditorConfig>;
};

/** MailEditor 邮件富文本编辑器，通过 key 强制重新挂载避免 WangEditor 重复创建错误。 */
export default function MailEditor({ value, onChange, toolbarConfig, editorConfig }: MailEditorProps) {
  const [editor, setEditor] = useState<IDomEditor | null>(null);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<HTMLDivElement>(null);

  // 组件卸载时销毁编辑器实例，防止 WangEditor 内部状态残留
  useEffect(() => {
    return () => {
      if (editor) {
        editor.destroy();
        setEditor(null);
      }
    };
  }, [editor]);

  return (
    <Box sx={{ border: "1px solid", borderColor: "divider", borderRadius: "8px", overflow: "hidden", "& .w-e-text-container": { minHeight: "260px !important" }, "& img": { maxWidth: "100%", height: "auto" } }}>
      <div ref={toolbarRef}>
        <Toolbar editor={editor} defaultConfig={toolbarConfig || {}} mode="default" style={{ borderBottom: "1px solid #eee" }} />
      </div>
      <div ref={editorRef}>
        <Editor defaultConfig={editorConfig || {}} value={value} onCreated={setEditor} onChange={(e) => onChange(e.getHtml())} mode="default" style={{ height: 320, overflowY: "hidden" }} />
      </div>
    </Box>
  );
}
