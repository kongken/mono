# Issue tracker

需求、任务和规格记录在 kongken/mono 的 GitHub Issues。
使用 gh CLI，并显式指定 --repo kongken/mono。

## 常用操作

- 创建：gh issue create --repo kongken/mono --title "标题" --body-file <文件>
- 阅读：gh issue view <编号> --repo kongken/mono --json number,title,body,labels,comments
- 列表：gh issue list --repo kongken/mono --state open
- 添加标签：gh issue edit <编号> --repo kongken/mono --add-label "<标签>"
- 移除标签：gh issue edit <编号> --repo kongken/mono --remove-label "<标签>"
- 评论：gh issue comment <编号> --repo kongken/mono --body-file <文件>
- 关闭：gh issue close <编号> --repo kongken/mono

多行正文写入临时文件，通过 --body-file 提交。
技能要求“发布到 issue tracker”时创建 GitHub Issue；
要求“读取相关 ticket”时读取对应 Issue。

## 父子任务与依赖

优先使用 GitHub 原生子 Issue 和依赖关系。
不可用时，在子任务正文顶部写 Part of #<父任务编号>，
并在父任务中维护任务清单；依赖关系写为 Blocked by: #<编号>。
所有阻塞任务关闭后，任务才解除阻塞。

## Pull requests as a triage surface

**PRs as a request surface: no.**
