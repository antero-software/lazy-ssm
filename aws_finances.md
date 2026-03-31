| Instance Type | Units    |
|---------------|----------|
| t2.nano       | 1 Unit   |
| t2.micro      | 2 Units  |
| t2.small      | 4 Units  |
| t2.medium     | 8 Units  |

| Account   | Server                | Instance Type | t2.nano Units | Keep | Notes                                                            |
|-----------|-----------------------|---------------|---------------|------|------------------------------------------------------------------|
| weareflip | Production-hic        | t2.medium     | 8             | Y    | Liklely keep for a lot longer                                    |
|           | CLH-Proxy             | t2.small      | 4             | Y    | Likely keep unless we want to convert to lambda                  |
|           | Pripark               | t2.medium     | 8             | N    | Likely replaced by t3 in security upgrades                       |
|           | Flipper Production    | t2.micro      | 2             | N    | Bastion - Should be removed                                      |
|           | Flip backups          | t2.micro      | 2             | N    | Should be removed - need to backup Pripark EFS another way first |
| citysmart | ecs-autoscaling-group | t2.medium     | 8             | N    | Can easily convert to t3, potentially t4g                        |
|           | ecs-autoscaling-group | t2.medium     | 8             | N    | Can easily convert to t3, potentially t4g                        |
|           | ecs-autoscaling-group | t2.medium     | 8             | N    | Can easily convert to t3, potentially t4g                        |
|           | uat-bastion-server    | t2.micro      | 2             | N    | Bastion - Should be removed                                      |

- 12 Units for the Production-hic and CLH-Proxy servers.
