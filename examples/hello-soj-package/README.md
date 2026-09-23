# Hello SOJ 测评包

这是一个安全的端到端烟雾测试包，不执行不可信的`submission/source`：当提交文件的内容精确为`Hello, SOJ!`并以换行结束时，返回`accepted`和`100`分；否则返回`wrong_answer`和`0`分。它用于验证题目包上传、Job 分发、Slurm 运行和结果回传。

为题目 4 构建可直接上传的 archive：

```sh
sh examples/hello-soj-package/package.sh 4
```

输出为`examples/hello-soj-package/problem-4-example.tar.zst`。archive 根目录直接包含`manifest.json`、`judge.sbatch`和`tests/`，可在 Admin 的题目 4 编辑页上传。

不要把它用于真实题目：真实测评包必须在独立的容器/受限环境中编译和运行用户代码，并保证用户代码无法修改`output/result.json`。
